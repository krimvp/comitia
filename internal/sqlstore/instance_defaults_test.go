package sqlstore

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/service"

	"github.com/krimvp/comitia/internal/model"
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
			d, err := model.InstanceDefaultsFromEnv()
			if tc.invalid {
				if !errors.Is(err, model.ErrInvalid) {
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
			if m := d.Message(&model.AgentInstance{ID: "override"}); m.ID != "override" || m.Label != "" {
				t.Fatalf("explicit message inherited label: %v", m)
			}
			if _, err := d.ID("a", "b"); !errors.Is(err, model.ErrInvalid) {
				t.Fatalf("multiple IDs: %v", err)
			}
		})
	}
}

func TestInstanceDedupeScopesAndLegacyMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dedupe.db")
	f := setup(t, path)
	ctx, cid, token := context.Background(), f.context.ID, f.admin.Token
	legacyInput := model.MessageInput{DedupeKey: "legacy", Body: "legacy"}
	workerInput := model.MessageInput{DedupeKey: "started", Body: "ready", AgentInstance: &model.AgentInstance{ID: "worker", Label: "Worker"}}
	legacy, err := f.svc.AppendMessage(ctx, token, cid, legacyInput)
	if err != nil {
		t.Fatal(err)
	}
	worker, err := f.svc.AppendMessage(ctx, token, cid, workerInput)
	if err != nil {
		t.Fatal(err)
	}
	// Restore pre-change physical keys and remove only the new side table.
	if _, err := DB(f.store).Exec("UPDATE messages SET dedupe_key=? WHERE id=?", legacy.DedupeKey, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := DB(f.store).Exec("UPDATE messages SET dedupe_key=? WHERE id=?", worker.DedupeKey, worker.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := DB(f.store).Exec("DROP TABLE message_dedupe_scopes"); err != nil {
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
		svc, err := service.New(model.Config{Store: st, Clock: func() time.Time { return *f.now }})
		if err != nil {
			t.Fatal(err)
		}
		for i, in := range []model.MessageInput{legacyInput, workerInput} {
			m, err := svc.AppendMessage(ctx, token, cid, in)
			if err != nil || m.ID != []string{legacy.ID, worker.ID}[i] || m.DedupeKey != in.DedupeKey {
				t.Fatalf("old retry: %v %v", m, err)
			}
		}
		for _, instance := range []*model.AgentInstance{nil, {ID: "reviewer"}} {
			in := model.MessageInput{DedupeKey: "started", Body: "another process", AgentInstance: instance}
			m, err := svc.AppendMessage(ctx, token, cid, in)
			if err != nil || m.ID == worker.ID || m.DedupeKey != "started" {
				t.Fatalf("independent scope: %v %v", m, err)
			}
			retry, err := svc.AppendMessage(ctx, token, cid, in)
			if err != nil || retry.ID != m.ID || retry.Sequence != m.Sequence {
				t.Fatalf("same-instance retry: %v %v", retry, err)
			}
			in.Body = "changed"
			if _, err := svc.AppendMessage(ctx, token, cid, in); !errors.Is(err, model.ErrConflict) {
				t.Fatalf("changed retry: %v", err)
			}
		}
		changed := workerInput
		changed.AgentInstance = &model.AgentInstance{ID: "worker", Label: "different"}
		if _, err := svc.AppendMessage(ctx, token, cid, changed); !errors.Is(err, model.ErrConflict) {
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
	_, err := f.svc.AppendMessage(ctx, token, cid, model.MessageInput{DedupeKey: "old-key", Body: "old"})
	if err != nil {
		t.Fatal(err)
	}
	// Another instance occupies the natural key, and an arbitrary legacy key
	// can also occupy the first opaque candidate.
	if _, err := f.svc.AppendMessage(ctx, token, cid, model.MessageInput{DedupeKey: "chosen-id", Body: "blocker"}); err != nil {
		t.Fatal(err)
	}
	var inserted model.Message
	err = f.store.Update(ctx, func(w model.WriteStore) error {
		var err error
		inserted, err = w.PutMessage(model.Message{ID: "chosen-id", ContextID: cid, SessionID: f.admin.Session.ID, DedupeKey: "old-key", Body: "new", CreatedAt: *f.now, AgentInstance: &model.AgentInstance{ID: "worker"}})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	in := model.MessageInput{DedupeKey: "old-key", Body: "new", AgentInstance: &model.AgentInstance{ID: "worker"}}
	retry, err := f.svc.AppendMessage(ctx, token, cid, in)
	if err != nil || retry.ID != inserted.ID || retry.DedupeKey != "old-key" {
		t.Fatalf("storage collision lost natural key: %v %v", retry, err)
	}
}

func TestInstanceDedupeDowngradeAndReupgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollback.db")
	f := setup(t, path)
	ctx, cid, token := context.Background(), f.context.ID, f.admin.Token
	in := model.MessageInput{DedupeKey: "started", Body: "ready", AgentInstance: &model.AgentInstance{ID: "worker"}}
	original, err := f.svc.AppendMessage(ctx, token, cid, in)
	if err != nil {
		t.Fatal(err)
	}
	var physicalKey string
	if err := DB(f.store).QueryRow("SELECT dedupe_key FROM messages WHERE id=?", original.ID).Scan(&physicalKey); err != nil || physicalKey != in.DedupeKey {
		t.Fatalf("old binary sees %q: %v", physicalKey, err)
	}
	// A pre-instance-dedupe binary finds the original through its unchanged
	// (context, session, key) query, so a retry does not append a new message.
	var oldID string
	if err := DB(f.store).QueryRow("SELECT id FROM messages WHERE context_id=? AND session_id=? AND dedupe_key=?", cid, original.SessionID, in.DedupeKey).Scan(&oldID); err != nil || oldID != original.ID {
		t.Fatalf("downgraded retry: %q %v", oldID, err)
	}
	// The old binary can append new messages, but knows nothing about scopes.
	if _, err := DB(f.store).Exec("INSERT INTO messages VALUES(?,?,?,?,?,?,?)", "old-binary-message", cid, original.SessionID, 2, "old-binary-key", "from old binary", model.TS(*f.now)); err != nil {
		t.Fatal(err)
	}
	if _, err := DB(f.store).Exec("INSERT INTO message_instances VALUES(?,?,?)", "old-binary-message", "worker", ""); err != nil {
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
		svc, err := service.New(model.Config{Store: st, Clock: func() time.Time { return *f.now }})
		if err != nil {
			t.Fatal(err)
		}
		retry, err := svc.AppendMessage(ctx, token, cid, in)
		if err != nil || retry.ID != original.ID || retry.Sequence != original.Sequence {
			t.Fatalf("re-upgraded retry: %v %v", retry, err)
		}
		oldRetry, err := svc.AppendMessage(ctx, token, cid, model.MessageInput{DedupeKey: "old-binary-key", Body: "from old binary", AgentInstance: &model.AgentInstance{ID: "worker"}})
		if err != nil || oldRetry.ID != "old-binary-message" || oldRetry.Sequence != 2 {
			t.Fatalf("backfilled old-binary retry: %v %v", oldRetry, err)
		}
		other := in
		other.AgentInstance = &model.AgentInstance{ID: "reviewer"}
		collision, err := svc.AppendMessage(ctx, token, cid, other)
		if err != nil || collision.ID == original.ID || collision.DedupeKey != "started" {
			t.Fatalf("colliding instance: %v %v", collision, err)
		}
		if err := DB(st).QueryRow("SELECT dedupe_key FROM messages WHERE id=?", collision.ID).Scan(&physicalKey); err != nil || physicalKey == "started" {
			t.Fatalf("collision storage key: %q %v", physicalKey, err)
		}
		var missing, count int
		if err := DB(st).QueryRow("SELECT COUNT(*) FROM messages m WHERE NOT EXISTS (SELECT 1 FROM message_dedupe_scopes d WHERE d.message_id=m.id)").Scan(&missing); err != nil || missing != 0 {
			t.Fatalf("messages without scopes: %d %v", missing, err)
		}
		if err := DB(st).QueryRow("SELECT COUNT(*) FROM messages").Scan(&count); err != nil || count != 3 {
			t.Fatalf("duplicate rows after rollback: %d %v", count, err)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInstanceDedupeBackfillRejectsConflictingRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conflicting-scopes.db")
	f := setup(t, path)
	ctx, cid, token := context.Background(), f.context.ID, f.admin.Token
	m, err := f.svc.AppendMessage(ctx, token, cid, model.MessageInput{DedupeKey: "started", Body: "ready", AgentInstance: &model.AgentInstance{ID: "worker"}})
	if err != nil {
		t.Fatal(err)
	}
	// Reproduce a duplicate left by the earlier opaque-key implementation
	// followed by an old-binary retry. Migration must not silently skip it.
	if _, err := DB(f.store).Exec("UPDATE messages SET dedupe_key=? WHERE id=?", m.ID, m.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := DB(f.store).Exec("INSERT INTO messages VALUES(?,?,?,?,?,?,?)", "duplicate", cid, m.SessionID, 2, "started", "ready", model.TS(*f.now)); err != nil {
		t.Fatal(err)
	}
	if _, err := DB(f.store).Exec("INSERT INTO message_instances VALUES(?,?,?)", "duplicate", "worker", ""); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := OpenSQLite(path)
	if err == nil {
		st.Close()
		t.Fatal("migration silently left a conflicting message without a scope")
	}
}
