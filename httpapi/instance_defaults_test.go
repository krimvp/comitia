package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

func TestRemoteInstanceDefaults(t *testing.T) {
	st, err := comitia.OpenSQLite(":memory:")
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
