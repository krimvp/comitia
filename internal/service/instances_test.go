package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/sqlstore"
)

func TestAgentInstancesPersistenceAndCursorIsolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instances.db")
	f := setup(t, path)
	ctx, cid, token := context.Background(), f.context.ID, f.admin.Token
	legacy, err := f.svc.AppendMessage(ctx, token, cid, model.MessageInput{DedupeKey: "legacy", Body: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.AckMessages(ctx, token, cid, legacy.Sequence); err != nil {
		t.Fatal(err)
	}
	// Emulate an existing database with messages and acknowledgements but no instance tables.
	if _, err = sqlstore.DB(f.store).Exec("DROP TABLE message_instances; DROP TABLE instance_acks"); err != nil {
		t.Fatal(err)
	}
	if err = f.store.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := sqlstore.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := New(model.Config{Store: st, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	instance := &model.AgentInstance{ID: "worker", Label: "Issue 3 worker"}
	in := model.MessageInput{DedupeKey: "worker-1", Body: "ready", AgentInstance: instance, Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientSessionIDs: []string{f.admin.Session.ID}, RecipientInstances: []string{"reviewer", "worker"}, Payload: json.RawMessage(`{}`)}}
	worker, err := svc.AppendMessage(ctx, token, cid, in)
	if err != nil {
		t.Fatal(err)
	}
	instance.Label = "mutated by caller"
	if worker.AgentInstance.Label != "Issue 3 worker" {
		t.Fatal("message aliases caller metadata")
	}
	instance.Label = "Issue 3 worker"
	in.Envelope.RecipientInstances = []string{"worker", "reviewer"}
	retry, err := svc.AppendMessage(ctx, token, cid, in)
	if err != nil || retry.ID != worker.ID {
		t.Fatalf("normalized retry: %v %v", retry, err)
	}
	in.AgentInstance = &model.AgentInstance{ID: "worker", Label: "changed"}
	if _, err = svc.AppendMessage(ctx, token, cid, in); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("changed label: %v", err)
	}
	reviewer, err := svc.AppendMessage(ctx, token, cid, model.MessageInput{DedupeKey: "reviewer-1", Body: "reviewing", AgentInstance: &model.AgentInstance{ID: "reviewer"}})
	if err != nil {
		t.Fatal(err)
	}
	if worker.SessionID != f.admin.Session.ID || reviewer.SessionID != worker.SessionID || !worker.AgentInstanceSelfReported || !reviewer.AgentInstanceSelfReported {
		t.Fatal("claim replaced authenticated identity or lost trust marker")
	}
	if err = svc.AckMessages(ctx, token, cid, reviewer.Sequence, "worker"); err != nil {
		t.Fatal(err)
	}
	if err = svc.AckMessages(ctx, token, cid, worker.Sequence, "reviewer"); err != nil {
		t.Fatal(err)
	}
	if err = svc.AckMessages(ctx, token, cid, legacy.Sequence, "worker"); err != nil {
		t.Fatal(err)
	}
	if err = svc.AckMessages(ctx, token, cid, reviewer.Sequence+1, "worker"); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("future cursor: %v", err)
	}
	// The same instance ID in another credentialed session has a separate cursor.
	f.svc = svc
	other := messageWorker(t, f, "other", cid)
	if got, err := svc.MessageCursor(ctx, other.Token, cid, "worker"); err != nil || got != 0 {
		t.Fatalf("other session cursor: %d %v", got, err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = sqlstore.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err = New(model.Config{Store: st, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		instance string
		want     int64
	}{{"", legacy.Sequence}, {"worker", reviewer.Sequence}, {"reviewer", worker.Sequence}, {"fresh", 0}} {
		got, err := svc.MessageCursor(ctx, token, cid, check.instance)
		if err != nil || got != check.want {
			t.Fatalf("cursor %q: %d want %d, %v", check.instance, got, check.want, err)
		}
	}
	if got, err := svc.MessageCursor(ctx, token, cid); err != nil || got != legacy.Sequence {
		t.Fatalf("legacy cursor: %d %v", got, err)
	}
	msgs, err := svc.ReplayMessages(ctx, other.Token, cid, 0, 100)
	if err != nil || len(msgs) != 3 {
		t.Fatalf("shared replay: %v %v", msgs, err)
	}
	if msgs[0].AgentInstance != nil || msgs[0].AgentInstanceSelfReported || msgs[1].AgentInstance.ID != "worker" || msgs[1].AgentInstance.Label != "Issue 3 worker" || !msgs[1].AgentInstanceSelfReported || msgs[2].AgentInstance.ID != "reviewer" || !sameEnvelope(msgs[1].Envelope, worker.Envelope) {
		t.Fatalf("persisted claims: %#v", msgs)
	}
	if _, err = svc.AppendMessage(ctx, "invalid-token", cid, model.MessageInput{DedupeKey: "spoof", Body: "x", AgentInstance: &model.AgentInstance{ID: f.admin.Session.ID}}); !errors.Is(err, model.ErrUnauthenticated) {
		t.Fatalf("claim granted access: %v", err)
	}
	if err = svc.RevokeSession(ctx, token, other.Session.ID); err != nil {
		t.Fatal(err)
	}
	if err = svc.AckMessages(ctx, other.Token, cid, 1, "worker"); !errors.Is(err, model.ErrUnauthenticated) {
		t.Fatalf("claim bypassed revocation: %v", err)
	}
}

func TestAgentInstanceValidation(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx, cid, token := context.Background(), f.context.ID, f.admin.Token
	for _, id := range []string{"", "with space", "a/b", "é", strings.Repeat("x", 65)} {
		if _, err := f.svc.AppendMessage(ctx, token, cid, model.MessageInput{DedupeKey: "bad", Body: "x", AgentInstance: &model.AgentInstance{ID: id}}); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("instance %q: %v", id, err)
		}
	}
	for _, label := range []string{strings.Repeat("x", 129), "line\nbreak", string([]byte{0xff})} {
		if _, err := f.svc.AppendMessage(ctx, token, cid, model.MessageInput{DedupeKey: "bad", Body: "x", AgentInstance: &model.AgentInstance{ID: "valid", Label: label}}); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("label: %v", err)
		}
	}
	for _, ids := range [][]string{{"bad id"}, {"a", "b"}} {
		if err := f.svc.AckMessages(ctx, token, cid, 0, ids...); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("ack validation: %v", err)
		}
		if _, err := f.svc.MessageCursor(ctx, token, cid, ids...); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("cursor validation: %v", err)
		}
	}
	for _, ids := range [][]string{{"bad id"}, {"a", "a"}, make([]string, 65)} {
		if _, err := f.svc.AppendMessage(ctx, token, cid, model.MessageInput{DedupeKey: "bad", Body: "x", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: ids, Payload: json.RawMessage(`{}`)}}); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("recipient validation: %v", err)
		}
	}
}
