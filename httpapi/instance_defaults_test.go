package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
)

func TestRemoteInstanceDefaults(t *testing.T) {
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "once"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "once", AdminName: "owner", SessionTTL: time.Hour})
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
	ts := httptest.NewServer(NewHandler(svc))
	defer ts.Close()
	t.Setenv("COMITIA_AGENT_INSTANCE", "worker")
	t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "Worker")
	worker, err := NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMITIA_AGENT_INSTANCE", "reviewer")
	t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "Reviewer")
	reviewer, err := NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	in := comitia.MessageInput{DedupeKey: "started", Body: "ready"}
	a, err := worker.AppendMessage(ctx, owner.Token, c.ID, in)
	if err != nil || a.AgentInstance.ID != "worker" || a.AgentInstance.Label != "Worker" || in.AgentInstance != nil {
		t.Fatalf("captured worker defaults: %v %v", a, err)
	}
	b, err := reviewer.AppendMessage(ctx, owner.Token, c.ID, in)
	if err != nil || a.ID == b.ID || b.AgentInstance.ID != "reviewer" {
		t.Fatalf("different processes collided: %v %v", b, err)
	}
	retry, err := worker.AppendMessage(ctx, owner.Token, c.ID, in)
	if err != nil || retry.ID != a.ID || retry.Sequence != a.Sequence {
		t.Fatalf("worker retry: %v %v", retry, err)
	}
	in.AgentInstance = &comitia.AgentInstance{ID: "explicit"}
	explicit, err := worker.AppendMessage(ctx, owner.Token, c.ID, in)
	if err != nil || explicit.AgentInstance.ID != "explicit" || explicit.AgentInstance.Label != "" {
		t.Fatalf("explicit instance: %v %v", explicit, err)
	}
	if err := worker.AckMessages(ctx, owner.Token, c.ID, b.Sequence); err != nil {
		t.Fatal(err)
	}
	if got, err := worker.MessageCursor(ctx, owner.Token, c.ID); err != nil || got != b.Sequence {
		t.Fatalf("default cursor: %d %v", got, err)
	}
	if got, err := reviewer.MessageCursor(ctx, owner.Token, c.ID); err != nil || got != 0 {
		t.Fatalf("reviewer cursor: %d %v", got, err)
	}
	if err := worker.AckMessages(ctx, owner.Token, c.ID, a.Sequence, "reviewer"); err != nil {
		t.Fatal(err)
	}
	if got, err := reviewer.MessageCursor(ctx, owner.Token, c.ID); err != nil || got != a.Sequence {
		t.Fatalf("explicit ack: %d %v", got, err)
	}
	if got, err := worker.MessageCursor(ctx, owner.Token, c.ID, ""); err != nil || got != 0 {
		t.Fatalf("legacy override: %d %v", got, err)
	}
	if err := worker.AckMessages(ctx, owner.Token, c.ID, a.Sequence, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := worker.MessageCursor(ctx, owner.Token, c.ID, ""); err != nil || got != a.Sequence {
		t.Fatalf("explicit legacy ack: %d %v", got, err)
	}
	for _, ids := range [][]string{{"bad id"}, {"a", "b"}} {
		if err := worker.AckMessages(ctx, owner.Token, c.ID, 0, ids...); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("invalid explicit ack: %v", err)
		}
		if _, err := worker.MessageCursor(ctx, owner.Token, c.ID, ids...); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("invalid explicit cursor: %v", err)
		}
	}
	for _, tc := range []struct {
		name      string
		client    *Client
		instances []comitia.AgentInstance
		want      *comitia.AgentInstance
		invalid   bool
	}{
		{name: "default", client: worker, want: &comitia.AgentInstance{ID: "worker", Label: "Worker"}},
		{name: "explicit", client: worker, instances: []comitia.AgentInstance{{ID: "explicit"}}, want: &comitia.AgentInstance{ID: "explicit"}},
		{name: "empty", client: worker, instances: []comitia.AgentInstance{{}}, invalid: true},
		{name: "invalid", client: worker, instances: []comitia.AgentInstance{{ID: "bad id"}}, invalid: true},
		{name: "multiple", client: worker, instances: []comitia.AgentInstance{{ID: "a"}, {ID: "b"}}, invalid: true},
	} {
		t.Run("claim-"+tc.name, func(t *testing.T) {
			task, err := tc.client.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: tc.name})
			if err != nil {
				t.Fatal(err)
			}
			claim, err := tc.client.ClaimTask(ctx, owner.Token, task.ID, time.Minute, tc.instances...)
			if tc.invalid {
				if !errors.Is(err, comitia.ErrInvalid) {
					t.Fatalf("claim: %v", err)
				}
			} else if err != nil || claim.Task.LeaseOwnerInstance == nil || *claim.Task.LeaseOwnerInstance != *tc.want {
				t.Fatalf("claim: %+v %v want=%v", claim, err, tc.want)
			}
		})
	}
	legacy, err := NewClientWithInstanceDefaults(ts.URL, ts.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	task, err := legacy.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := legacy.ClaimTask(ctx, owner.Token, task.ID, time.Minute)
	if err != nil || claim.Task.LeaseOwnerInstance != nil {
		t.Fatalf("legacy claim: %+v %v", claim, err)
	}
	if _, err := legacy.ClaimTask(ctx, owner.Token, task.ID, time.Minute, comitia.AgentInstance{}); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("empty legacy claim: %v", err)
	}
	presence, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: w.ID, Name: "presence"})
	if err != nil {
		t.Fatal(err)
	}
	none := ""
	if _, err := worker.ReplayMessages(ctx, owner.Token, presence.ID, 0, 10, comitia.MessageFilters{AgentInstance: &none}); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.WaitMessages(ctx, owner.Token, presence.ID, 0, 0, comitia.MessageFilters{AgentInstance: &none}); err != nil {
		t.Fatal(err)
	}
	if rows, err := svc.ListInstances(ctx, owner.Token, presence.ID); err != nil || len(rows) != 0 {
		t.Fatalf("explicit empty instance recorded presence: %v %v", rows, err)
	}
	if _, err := worker.ReplayMessages(ctx, owner.Token, presence.ID, 0, 10); err != nil {
		t.Fatal(err)
	}
	awaitReadHeartbeat(t, func() error {
		_, err := reviewer.WaitMessages(ctx, owner.Token, presence.ID, 0, 0, comitia.MessageFilters{})
		return err
	}, func() ([]comitia.InstancePresence, error) { return svc.ListInstances(ctx, owner.Token, presence.ID) }, func(rows []comitia.InstancePresence) bool {
		return slices.ContainsFunc(rows, func(p comitia.InstancePresence) bool { return p.ID == "reviewer" })
	})
	if err := worker.SetInstanceResume(ctx, owner.Token, presence.ID, nil, []byte(`{"pid":1}`)); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.ListInstances(ctx, owner.Token, presence.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("default presence: %v %v", rows, err)
	}
	for _, row := range rows {
		if row.ID == "worker" && (row.Label != "Worker" || string(row.Resume) != `{"pid":1}`) || row.ID != "worker" && row.ID != "reviewer" {
			t.Fatalf("default presence row: %+v", row)
		}
	}
}

func TestRemoteClientRejectsInvalidInstanceEnvironment(t *testing.T) {
	for _, tc := range []struct{ id, label string }{{"bad id", ""}, {"", "orphan"}, {"ok", "bad\nlabel"}} {
		t.Setenv("COMITIA_AGENT_INSTANCE", tc.id)
		t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", tc.label)
		if _, err := NewClient("http://localhost:8080", nil); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("startup accepted %q/%q: %v", tc.id, tc.label, err)
		}
	}
}

func TestRemoteClientExplicitDefaultsIgnoreEnvironment(t *testing.T) {
	t.Setenv("COMITIA_AGENT_INSTANCE", "bad id")
	t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "orphan\nlabel")
	ts := httptest.NewServer(NewHandler(fakeAPI{}))
	defer ts.Close()
	client, err := NewClientWithInstanceDefaults(ts.URL, ts.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	if id, err := client.InstanceDefaults.ID(); err != nil || id != "" {
		t.Fatalf("explicit zero default: %q %v", id, err)
	}
	if w, err := client.CreateWorkspace(context.Background(), "admin", "admin"); err != nil || w.Name != "admin" {
		t.Fatalf("remote admin: %v %v", w, err)
	}
}
