package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/sqlite"
)

func TestHTTPInstancePresence(t *testing.T) {
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
	server := httptest.NewServer(NewHandler(svc))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := client.ListInstances(ctx, owner.Token, c.ID)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty: %v %v", rows, err)
	}
	resume := json.RawMessage(`{"harness":"claude","session_id":"harness","pid":123,"large":9007199254740993}`)
	if err := client.SetInstanceResume(ctx, owner.Token, c.ID, &comitia.AgentInstance{ID: "worker", Label: "HTTP worker"}, resume); err != nil {
		t.Fatal(err)
	}
	worker := "worker"
	list := func() ([]comitia.InstancePresence, error) { return client.ListInstances(ctx, owner.Token, c.ID) }
	roundtrip := func(rows []comitia.InstancePresence) bool {
		return len(rows) == 1 && rows[0].Label == "HTTP worker" && rows[0].Active && string(rows[0].Resume) == string(resume) && rows[0].LastSeen.Equal(now)
	}
	for _, step := range []struct {
		read bool
		call func() error
	}{
		{call: func() error {
			_, err := client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "x", Body: "x", AgentInstance: &comitia.AgentInstance{ID: "worker", Label: "HTTP worker"}})
			return err
		}},
		{read: true, call: func() error {
			_, err := client.ReplayMessages(ctx, owner.Token, c.ID, 0, 10, comitia.MessageFilters{AgentInstance: &worker})
			return err
		}},
		{read: true, call: func() error { _, err := client.MessageCursor(ctx, owner.Token, c.ID, "worker"); return err }},
		{call: func() error { return client.AckMessages(ctx, owner.Token, c.ID, 0, "worker") }},
	} {
		now = now.Add(31 * time.Second)
		if step.read {
			awaitReadHeartbeat(t, step.call, list, roundtrip)
			continue
		}
		if err := step.call(); err != nil {
			t.Fatal(err)
		}
		// Writes record presence in their own transaction, with no budget.
		if rows, err := list(); err != nil || !roundtrip(rows) {
			t.Fatalf("roundtrip: %v %v", rows, err)
		}
	}
	checkTaskPresence := func() {
		t.Helper()
		rows, err := client.ListInstances(ctx, owner.Token, c.ID)
		if err != nil || len(rows) != 1 || rows[0].SeenSecondsAgo == nil || *rows[0].SeenSecondsAgo != 0 || rows[0].Recent == nil || !*rows[0].Recent || !rows[0].LastSeen.Equal(now) {
			t.Fatalf("task presence: %v %v", rows, err)
		}
	}
	task, err := client.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(31 * time.Second)
	claim, err := client.ClaimTask(ctx, owner.Token, task.ID, time.Hour, comitia.AgentInstance{ID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	checkTaskPresence()
	now = now.Add(31 * time.Second)
	claim, err = client.RenewClaim(ctx, owner.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	checkTaskPresence()
	now = now.Add(31 * time.Second)
	if _, err := client.CompleteTask(ctx, owner.Token, task.ID, claim.Token, claim.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	checkTaskPresence()
	now = now.Add(90 * time.Second)
	rows, err = client.ListInstances(ctx, owner.Token, c.ID)
	if err != nil || len(rows) != 1 || rows[0].SeenSecondsAgo == nil || *rows[0].SeenSecondsAgo != 90 || rows[0].Recent == nil || *rows[0].Recent || !rows[0].Active {
		t.Fatalf("stale presence: %v %v", rows, err)
	}
	if err := client.SetInstanceResume(ctx, owner.Token, c.ID, &comitia.AgentInstance{ID: "worker"}, json.RawMessage(`[]`)); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}
	if _, err := client.ListInstances(ctx, "invalid", c.ID); !errors.Is(err, comitia.ErrUnauthenticated) {
		t.Fatalf("auth: %v", err)
	}
	if _, err := client.ListInstances(ctx, owner.Token, "unreadable"); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("scope: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/instances/resume", strings.NewReader(`{"context_id":"`+c.ID+`","instance":{"id":"worker","unknown":true},"resume":{}}`))
	req.Header.Set("Authorization", "Bearer "+owner.Token)
	req.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", response.StatusCode)
	}
}

// awaitReadHeartbeat repeats a read until list shows the presence it records.
// A read's heartbeat is best effort under a 25 ms real-time budget
// (docs/presence.md), which a slow machine (-race, a loaded runner) can exceed
// without contention. A dropped heartbeat leaves the row stale, so the next
// read is due again and records the same presence.
func awaitReadHeartbeat(t *testing.T, read func() error, list func() ([]comitia.InstancePresence, error), recorded func([]comitia.InstancePresence) bool) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); ; {
		if err := read(); err != nil {
			t.Fatal(err)
		}
		rows, err := list()
		if err == nil && recorded(rows) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("read heartbeat not recorded: %v %v", rows, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
