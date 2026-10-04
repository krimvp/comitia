package comitia

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstanceDefaultsEnvironment(t *testing.T) {
	for _, tc := range []struct {
		id, label string
		invalid   bool
	}{
		{}, {id: "worker", label: "Worker α"},
		{id: "bad id", invalid: true}, {id: strings.Repeat("x", 65), invalid: true},
		{label: "orphan", invalid: true}, {id: "ok", label: "line\nbreak", invalid: true},
		{id: "ok", label: strings.Repeat("x", 129), invalid: true},
		{id: "ok", label: string([]byte{0xff}), invalid: true},
	} {
		t.Run(tc.id+tc.label, func(t *testing.T) {
			t.Setenv("COMITIA_AGENT_INSTANCE", tc.id)
			t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", tc.label)
			d, err := InstanceDefaultsFromEnv()
			if tc.invalid {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("invalid environment: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			id, err := d.ID()
			if err != nil || id != tc.id {
				t.Fatalf("default ID: %q %v", id, err)
			}
			if tc.id == "" {
				if d.Message(nil) != nil {
					t.Fatal("unset environment changed legacy behavior")
				}
			} else {
				m := d.Message(nil)
				if m.ID != tc.id || m.Label != tc.label {
					t.Fatalf("default message: %v", m)
				}
				m.Label = "mutated"
				if d.Message(nil).Label != tc.label {
					t.Fatal("default aliases returned metadata")
				}
			}
			for _, explicit := range []string{"", "override"} {
				if id, err := d.ID(explicit); err != nil || id != explicit {
					t.Fatalf("explicit ID: %q %v", id, err)
				}
			}
			if m := d.Message(&AgentInstance{ID: "override"}); m.ID != "override" || m.Label != "" {
				t.Fatalf("explicit message inherited label: %v", m)
			}
			if _, err := d.ID("a", "b"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("multiple IDs: %v", err)
			}
		})
	}
}

func TestInstanceDedupeScopesAndLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dedupe.db")
	f := setup(t, path)
	ctx, cid, token := context.Background(), f.context.ID, f.admin.Token
	legacyInput := MessageInput{DedupeKey: "legacy", Body: "legacy"}
	workerInput := MessageInput{DedupeKey: "started", Body: "ready", AgentInstance: &AgentInstance{ID: "worker", Label: "Worker"}}
	legacy, err := f.svc.AppendMessage(ctx, token, cid, legacyInput)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := f.svc.AppendMessage(ctx, token, cid, workerInput)
	if err != nil {
		t.Fatal(err)
	}
	// Restore pre-change physical keys and remove only the new side table.
	if _, err := f.store.db.Exec("UPDATE messages SET dedupe_key=? WHERE id=?", legacy.DedupeKey, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec("UPDATE messages SET dedupe_key=? WHERE id=?", worker.DedupeKey, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.db.Exec("DROP TABLE message_dedupe_scopes"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	for reopen := 0; reopen < 2; reopen++ {
		st, err := OpenSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		svc, err := New(Config{Store: st, Clock: func() time.Time { return *f.now }})
		if err != nil {
			t.Fatal(err)
		}
		for i, in := range []MessageInput{legacyInput, workerInput} {
			m, err := svc.AppendMessage(ctx, token, cid, in)
			if err != nil || m.ID != []string{legacy.ID, worker.ID}[i] || m.DedupeKey != in.DedupeKey {
				t.Fatalf("old retry: %v %v", m, err)
			}
		}
		for _, instance := range []*AgentInstance{nil, {ID: "reviewer"}} {
			in := MessageInput{DedupeKey: "started", Body: "another process", AgentInstance: instance}
			m, err := svc.AppendMessage(ctx, token, cid, in)
			if err != nil || m.ID == worker.ID || m.DedupeKey != "started" {
				t.Fatalf("independent scope: %v %v", m, err)
			}
			retry, err := svc.AppendMessage(ctx, token, cid, in)
			if err != nil || retry.ID != m.ID || retry.Sequence != m.Sequence {
				t.Fatalf("same-instance retry: %v %v", retry, err)
			}
			in.Body = "changed"
			if _, err := svc.AppendMessage(ctx, token, cid, in); !errors.Is(err, ErrConflict) {
				t.Fatalf("changed retry: %v", err)
			}
		}
		changed := workerInput
		changed.AgentInstance = &AgentInstance{ID: "worker", Label: "different"}
		if _, err := svc.AppendMessage(ctx, token, cid, changed); !errors.Is(err, ErrConflict) {
			t.Fatalf("changed label: %v", err)
		}
		msgs, err := svc.ReplayMessages(ctx, token, cid, 0, 100)
		if err != nil || len(msgs) != 4 || msgs[1].DedupeKey != "started" || msgs[3].DedupeKey != "started" {
			t.Fatalf("natural keys after reopen: %v %v", msgs, err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstanceDedupeStorageKeyCollision(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx, cid, token := context.Background(), f.context.ID, f.admin.Token
	old, err := f.svc.AppendMessage(ctx, token, cid, MessageInput{DedupeKey: "old-key", Body: "old"})
	if err != nil {
		t.Fatal(err)
	}
	// Arbitrary legacy keys can equal an internal key chosen for a new message.
	if _, err := f.store.db.Exec("UPDATE messages SET dedupe_key='chosen-id' WHERE id=?", old.ID); err != nil {
		t.Fatal(err)
	}
	var inserted Message
	err = f.store.Update(ctx, func(w WriteStore) error {
		var err error
		inserted, err = w.PutMessage(Message{ID: "chosen-id", ContextID: cid, SessionID: f.admin.Session.ID, DedupeKey: "new-key", Body: "new", CreatedAt: *f.now, AgentInstance: &AgentInstance{ID: "worker"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	in := MessageInput{DedupeKey: "new-key", Body: "new", AgentInstance: &AgentInstance{ID: "worker"}}
	retry, err := f.svc.AppendMessage(ctx, token, cid, in)
	if err != nil || retry.ID != inserted.ID || retry.DedupeKey != "new-key" {
		t.Fatalf("storage collision lost natural key: %v %v", retry, err)
	}
}
