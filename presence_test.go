package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstancePresenceHeartbeatsAndResume(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx, raw, cid := context.Background(), f.admin.Token, f.context.ID
	instance := &AgentInstance{ID: "worker", Label: "Worker"}
	in := MessageInput{DedupeKey: "ready", Body: "ready", AgentInstance: instance}
	check := func() {
		t.Helper()
		rows, err := f.svc.ListInstances(ctx, raw, cid)
		if err != nil || len(rows) != 1 {
			t.Fatalf("presence: %v %v", rows, err)
		}
		p := rows[0]
		if p.ID != instance.ID || p.Label != instance.Label || p.SessionID != f.admin.Session.ID || p.ContextID != cid || !p.LastSeen.Equal(*f.now) {
			t.Fatalf("presence: %+v", p)
		}
	}
	m, err := f.svc.AppendMessage(ctx, raw, cid, in)
	if err != nil {
		t.Fatal(err)
	}
	check()
	*f.now = f.now.Add(time.Second)
	if _, err := f.svc.AppendMessage(ctx, raw, cid, in); err != nil {
		t.Fatal(err)
	}
	check()
	resume := json.RawMessage(`{ "harness":"codex", "session_id":"harness-session", "cwd":"/tmp/worker", "pid":123, "extension":{"integer":9007199254740993} }`)
	*f.now = f.now.Add(time.Second)
	if err := f.svc.SetInstanceResume(ctx, raw, cid, instance, resume); err != nil {
		t.Fatal(err)
	}
	check()
	calls := []func() error{
		func() error {
			rows, err := f.svc.ReplayMessages(ctx, raw, cid, m.Sequence, 100, instanceFilter(instance.ID))
			if len(rows) != 0 {
				t.Fatal("expected empty replay")
			}
			return err
		},
		func() error { _, err := f.svc.MessageCursor(ctx, raw, cid, instance.ID); return err },
		func() error { return f.svc.AckMessages(ctx, raw, cid, m.Sequence, instance.ID) },
	}
	for _, call := range calls {
		*f.now = f.now.Add(time.Second)
		if err := call(); err != nil {
			t.Fatal(err)
		}
		check()
		rows, _ := f.svc.ListInstances(ctx, raw, cid)
		if string(rows[0].Resume) != string(resume) {
			t.Fatalf("resume changed: %s", rows[0].Resume)
		}
	}
	if err := f.svc.SetInstanceResume(ctx, raw, cid, &AgentInstance{ID: instance.ID, Label: "New label"}, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	rows, _ := f.svc.ListInstances(ctx, raw, cid)
	if rows[0].Label != "New label" || string(rows[0].Resume) != "{}" {
		t.Fatalf("resume replacement: %v", rows)
	}
}

func TestInstancePresenceScopeAndAuthorization(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx, raw, cid := context.Background(), f.admin.Token, f.context.ID
	_, reader, _, err := f.svc.DelegateSession(ctx, raw, DelegateInput{PrincipalName: "reader", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: cid, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.svc.CreateContext(ctx, raw, CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ token, context string }{{raw, cid}, {reader.Token, cid}, {raw, other.ID}} {
		if err := f.svc.SetInstanceResume(ctx, entry.token, entry.context, &AgentInstance{ID: "same"}, json.RawMessage(`{"pid":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := f.svc.ListInstances(ctx, reader.Token, cid)
	if err != nil || len(rows) != 2 || rows[0].SessionID == rows[1].SessionID {
		t.Fatalf("sessions: %v %v", rows, err)
	}
	for _, p := range rows {
		if p.ContextID != cid {
			t.Fatalf("cross-context row: %v", p)
		}
	}
	for _, call := range []func() error{
		func() error { _, err := f.svc.ListInstances(ctx, reader.Token, other.ID); return err },
		func() error {
			return f.svc.SetInstanceResume(ctx, reader.Token, other.ID, &AgentInstance{ID: "forbidden"}, json.RawMessage(`{}`))
		},
		func() error {
			_, err := f.svc.ReplayMessages(ctx, reader.Token, other.ID, 0, 10, instanceFilter("forbidden"))
			return err
		},
		func() error { _, err := f.svc.MessageCursor(ctx, reader.Token, other.ID, "forbidden"); return err },
		func() error { return f.svc.AckMessages(ctx, reader.Token, other.ID, 0, "forbidden") },
		func() error {
			_, err := f.svc.AppendMessage(ctx, reader.Token, cid, MessageInput{DedupeKey: "x", Body: "x", AgentInstance: &AgentInstance{ID: "forbidden"}})
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrForbidden) {
			t.Fatalf("authorization: %v", err)
		}
	}
	rows, _ = f.svc.ListInstances(ctx, raw, other.ID)
	if len(rows) != 1 {
		t.Fatalf("failed calls wrote presence: %v", rows)
	}
	if _, err := f.svc.ListInstances(ctx, "invalid", cid); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("auth: %v", err)
	}
	// Naming another session's ID as an instance never grants access.
	if _, err := f.svc.ReplayMessages(ctx, reader.Token, other.ID, 0, 10, instanceFilter(f.admin.Session.ID)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("spoofed authority: %v", err)
	}
	if err := f.svc.RevokeSession(ctx, raw, reader.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ListInstances(ctx, reader.Token, cid); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked: %v", err)
	}
	rows, _ = f.svc.ListInstances(ctx, raw, cid)
	if len(rows) != 2 {
		t.Fatal("historical presence lost on revocation")
	}
}

func TestInstancePresenceValidationAndLegacyCalls(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx, raw, cid := context.Background(), f.admin.Token, f.context.ID
	instance := &AgentInstance{ID: "worker"}
	for _, resume := range []string{"", "null", "[]", `"text"`, "1", "{} {}", `{"x":`, `{"x":"` + strings.Repeat("a", MaxInstanceResumeBytes) + `"}`} {
		if err := f.svc.SetInstanceResume(ctx, raw, cid, instance, json.RawMessage(resume)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("resume %q: %v", resume, err)
		}
	}
	for _, instance := range []*AgentInstance{nil, {ID: ""}, {ID: "bad space"}, {ID: "a", Label: "bad\nlabel"}, {ID: "a", Label: strings.Repeat("x", 129)}} {
		if err := f.svc.SetInstanceResume(ctx, raw, cid, instance, json.RawMessage(`{}`)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("instance: %v", err)
		}
	}
	boundary := json.RawMessage(`{"x":"` + strings.Repeat("a", MaxInstanceResumeBytes-8) + `"}`)
	if len(boundary) != MaxInstanceResumeBytes {
		t.Fatal("invalid boundary fixture")
	}
	if err := f.svc.SetInstanceResume(ctx, raw, cid, instance, boundary); err != nil {
		t.Fatalf("size boundary: %v", err)
	}
	if _, err := f.store.db.Exec("DELETE FROM instance_presence"); err != nil {
		t.Fatal(err)
	}
	for _, filters := range [][]MessageFilters{{instanceFilter("bad space")}, {instanceFilter("one"), instanceFilter("two")}} {
		if _, err := f.svc.ReplayMessages(ctx, raw, cid, 0, 10, filters...); !errors.Is(err, ErrInvalid) {
			t.Fatalf("replay IDs: %v", err)
		}
	}
	if _, err := f.svc.AppendMessage(ctx, raw, cid, MessageInput{DedupeKey: "legacy", Body: "legacy"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ReplayMessages(ctx, raw, cid, 0, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.MessageCursor(ctx, raw, cid); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AckMessages(ctx, raw, cid, 0); err != nil {
		t.Fatal(err)
	}
	rows, err := f.svc.ListInstances(ctx, raw, cid)
	if err != nil || len(rows) != 0 || rows == nil {
		t.Fatalf("legacy or invalid call wrote presence: %v %v", rows, err)
	}
}

type failingPresenceStore struct {
	Store
	upserts int
}
type failingPresenceWriter struct {
	WriteStore
	store *failingPresenceStore
}

func (s *failingPresenceStore) Update(ctx context.Context, fn func(WriteStore) error) error {
	return s.Store.Update(ctx, func(w WriteStore) error { return fn(&failingPresenceWriter{w, s}) })
}
func (w *failingPresenceWriter) PutInstancePresence(InstancePresence) error {
	w.store.upserts++
	return errors.New("presence write failed")
}
func TestInstancePresenceFailureDoesNotFailMessages(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	broken := &failingPresenceStore{Store: f.store}
	f.svc.store = broken
	ctx, raw, cid := context.Background(), f.admin.Token, f.context.ID
	m, err := f.svc.AppendMessage(ctx, raw, cid, MessageInput{DedupeKey: "x", Body: "x", AgentInstance: &AgentInstance{ID: "worker"}})
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := f.svc.ReplayMessages(ctx, raw, cid, 0, 10, instanceFilter("worker")); err != nil || len(rows) != 1 {
		t.Fatalf("replay: %v %v", rows, err)
	}
	if err := f.svc.AckMessages(ctx, raw, cid, m.Sequence, "worker"); err != nil {
		t.Fatal(err)
	}
	if cursor, err := f.svc.MessageCursor(ctx, raw, cid, "worker"); err != nil || cursor != m.Sequence {
		t.Fatalf("cursor: %v %v", cursor, err)
	}
	if broken.upserts != 4 {
		t.Fatalf("heartbeat upserts: %d", broken.upserts)
	}
	if err := f.svc.SetInstanceResume(ctx, raw, cid, &AgentInstance{ID: "worker"}, json.RawMessage(`{}`)); err == nil {
		t.Fatal("explicit write suppressed storage error")
	}
}

func TestInstancePresenceMigrationAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "presence.db")
	f := setup(t, path)
	// Simulate the previous schema and reopen: migration only creates a side table.
	if _, err := f.store.db.Exec("DROP TABLE instance_presence"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	svc, _ := New(Config{Store: st, Clock: func() time.Time { return *f.now }})
	ctx := context.Background()
	if err := svc.SetInstanceResume(ctx, f.admin.Token, f.context.ID, &AgentInstance{ID: "worker", Label: "saved"}, json.RawMessage(`{"pid":123}`)); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, _ = New(Config{Store: st, Clock: func() time.Time { return *f.now }})
	rows, err := svc.ListInstances(ctx, f.admin.Token, f.context.ID)
	if err != nil || len(rows) != 1 || rows[0].Label != "saved" || string(rows[0].Resume) != `{"pid":123}` {
		t.Fatalf("reopen: %v %v", rows, err)
	}
}

func instanceFilter(id string) MessageFilters { return MessageFilters{AgentInstance: &id} }
