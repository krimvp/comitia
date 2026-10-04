package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

func TestMCPInstancePresence(t *testing.T) {
	st, err := comitia.OpenSQLite(":memory:")
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
	for _, operation := range []struct {
		name string
		args map[string]any
	}{
		{"send_message", map[string]any{"agent_instance": map[string]any{"id": "worker", "label": "MCP worker"}, "dedupe_key": "x", "body": "x"}},
		{"replay_messages", map[string]any{"agent_instance": "worker"}},
		{"message_cursor", map[string]any{"agent_instance": "worker"}},
		{"ack_messages", map[string]any{"agent_instance": "worker", "sequence": 0}},
	} {
		now = now.Add(31 * time.Second)
		call(operation.name, operation.args)
		rows := call("list_instances", map[string]any{}).([]comitia.InstancePresence)
		if len(rows) != 1 || rows[0].ID != "worker" || rows[0].Label != "MCP worker" || !rows[0].LastSeen.Equal(now) || rows[0].SessionID != owner.Session.ID || !rows[0].Active || string(rows[0].Resume) != `{"harness":"codex","pid":123}` {
			t.Fatalf("%s: %v", operation.name, rows)
		}
	}
	for _, args := range []string{`{"context_id":"` + c.ID + `","resume":{}}`, `{"context_id":"` + c.ID + `","agent_instance":{"id":"worker"},"resume":null}`, `{"context_id":"` + c.ID + `","agent_instance":{"id":"worker"},"resume":{},"unknown":true}`} {
		if _, err := s.call(ctx, "set_instance_resume", json.RawMessage(args)); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("invalid resume accepted: %v", err)
		}
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
