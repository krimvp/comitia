package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/sqlite"
)

func TestMCPInstancePresence(t *testing.T) {
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "local", Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "local", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w, err := svc.CreateWorkspace(ctx, owner.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: w.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{API: svc, Credential: owner.Token}
	call := func(name string, args map[string]any) any {
		t.Helper()
		args["context_id"] = c.ID
		raw, _ := json.Marshal(args)
		v, err := s.call(ctx, name, raw)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if rows := call("list_instances", map[string]any{}).([]comitia.InstancePresence); len(rows) != 0 {
		t.Fatalf("initial: %v", rows)
	}
	call("set_instance_resume", map[string]any{"agent_instance": map[string]any{"id": "worker", "label": "MCP worker"}, "resume": map[string]any{"harness": "codex", "pid": 123}})
	recorded := func(rows []comitia.InstancePresence) bool {
		return len(rows) == 1 && rows[0].ID == "worker" && rows[0].Label == "MCP worker" && rows[0].LastSeen.Equal(now) && rows[0].SessionID == owner.Session.ID && rows[0].Active && string(rows[0].Resume) == `{"harness":"codex","pid":123}`
	}
	for _, operation := range []struct {
		name string
		args map[string]any
		read bool
	}{
		{"send_message", map[string]any{"agent_instance": map[string]any{"id": "worker", "label": "MCP worker"}, "dedupe_key": "x", "body": "x"}, false},
		{"replay_messages", map[string]any{"agent_instance": "worker"}, true},
		{"message_cursor", map[string]any{"agent_instance": "worker"}, true},
		{"ack_messages", map[string]any{"agent_instance": "worker", "sequence": 0}, false},
	} {
		now = now.Add(31 * time.Second)
		// A read's heartbeat is best effort under a 25 ms real-time budget
		// (docs/presence.md) that a slow machine can exceed without contention;
		// a dropped one leaves the row stale, so the next read records it.
		// Writes record presence in their own transaction, with no budget.
		deadline := time.Now().Add(30 * time.Second)
		for {
			call(operation.name, operation.args)
			rows := call("list_instances", map[string]any{}).([]comitia.InstancePresence)
			if recorded(rows) {
				break
			}
			if !operation.read || time.Now().After(deadline) {
				t.Fatalf("%s: %v", operation.name, rows)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	callTask := func(name string, args map[string]any) any {
		t.Helper()
		if name == "create_task" {
			args["context_id"] = c.ID
		}
		raw, _ := json.Marshal(args)
		v, err := s.call(ctx, name, raw)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	checkTaskPresence := func() {
		t.Helper()
		rows := call("list_instances", map[string]any{}).([]comitia.InstancePresence)
		if len(rows) != 1 || rows[0].SeenSecondsAgo == nil || *rows[0].SeenSecondsAgo != 0 || rows[0].Recent == nil || !*rows[0].Recent || !rows[0].LastSeen.Equal(now) {
			t.Fatalf("task presence: %v", rows)
		}
	}
	task := callTask("create_task", map[string]any{"title": "work"}).(comitia.Task)
	now = now.Add(31 * time.Second)
	claim := callTask("claim_task", map[string]any{"task_id": task.ID, "lease_seconds": 3600, "agent_instance": map[string]any{"id": "worker"}}).(comitia.TaskClaim)
	checkTaskPresence()
	now = now.Add(31 * time.Second)
	claim = callTask("renew_task", map[string]any{"task_id": task.ID, "lease_seconds": 3600, "claim_token": claim.Token, "lease_version": claim.Task.LeaseVersion}).(comitia.TaskClaim)
	checkTaskPresence()
	now = now.Add(31 * time.Second)
	callTask("complete_task", map[string]any{"task_id": task.ID, "claim_token": claim.Token, "lease_version": claim.Task.LeaseVersion})
	checkTaskPresence()
	for _, args := range []string{`{"context_id":"` + c.ID + `","resume":{}}`, `{"context_id":"` + c.ID + `","agent_instance":{"id":"worker"},"resume":null}`, `{"context_id":"` + c.ID + `","agent_instance":{"id":"worker"},"resume":{},"unknown":true}`} {
		if _, err := s.call(ctx, "set_instance_resume", json.RawMessage(args)); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("invalid resume accepted: %v", err)
		}
	}
	call("set_instance_resume", map[string]any{"agent_instance": map[string]any{"id": "second"}, "resume": map[string]any{}})
	page := call("list_instances", map[string]any{"limit": 1, "page": true}).(comitia.InstancePage)
	if len(page.Instances) != 1 || page.Instances[0].ID != "second" || !page.HasMore {
		t.Fatalf("page: %+v", page)
	}
	if page = call("list_instances", map[string]any{"after_id": page.NextAfter}).(comitia.InstancePage); len(page.Instances) != 1 || page.Instances[0].ID != "worker" || page.HasMore {
		t.Fatalf("next page: %+v", page)
	}
	if rows := call("list_instances", map[string]any{"limit": 1}).([]comitia.InstancePresence); len(rows) != 1 {
		t.Fatalf("limited list: %v", rows)
	}
	if _, err := s.call(ctx, "list_instances", json.RawMessage(`{"context_id":"`+c.ID+`","limit":0}`)); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("limit: %v", err)
	}
	s.Credential = "invalid"
	if _, err := s.call(ctx, "list_instances", json.RawMessage(`{"context_id":"`+c.ID+`"}`)); !errors.Is(err, comitia.ErrUnauthenticated) {
		t.Fatalf("auth: %v", err)
	}
	found := map[string]bool{}
	for _, tool := range tools() {
		found[tool["name"].(string)] = true
	}
	if !found["list_instances"] || !found["set_instance_resume"] {
		t.Fatal("presence tools not advertised")
	}
}
