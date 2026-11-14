package sqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/krimvp/comitia/internal/model"
)

func TestMessageProjectionMissingTriggerRecovery(t *testing.T) {
	for _, trigger := range []string{"comitia_message_filter_insert", "comitia_message_envelope_filter_insert", "comitia_message_instance_filter_insert"} {
		t.Run(trigger, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "messages.db")
			f := setup(t, path)
			defer func() { f.store.Close() }()
			ctx := context.Background()
			task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "work"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DB(f.store).Exec("DROP TRIGGER " + trigger); err != nil {
				t.Fatal(err)
			}
			m, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "missing-trigger", Body: "ready", AgentInstance: &model.AgentInstance{ID: "worker"}, Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", TaskID: task.ID, RecipientInstances: []string{"other"}, RecipientSessionIDs: []string{f.admin.Session.ID}, Payload: json.RawMessage(`{}`)}})
			if err != nil {
				t.Fatal(err)
			}
			if !MigrationPending(f.store, ctx) {
				t.Fatal("missing projection trigger not detected")
			}
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
			f.store, err = OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			if MigrationPending(f.store, ctx) {
				t.Fatal("recovered projection schema remains pending")
			}
			if err := f.store.View(ctx, func(r model.ReadStore) error {
				for _, tc := range []struct {
					f     model.MessageFilters
					count int
				}{
					{model.MessageFilters{ForInstance: "reader"}, 0},
					{model.MessageFilters{ForInstance: "other"}, 1},
					{model.MessageFilters{Schema: "comitia.discussion.v1"}, 1},
					{model.MessageFilters{TaskID: task.ID}, 1},
					{model.MessageFilters{FromInstance: "worker"}, 1},
					{model.MessageFilters{ForInstance: "other", Schema: "comitia.discussion.v1", TaskID: task.ID, FromInstance: "worker"}, 1},
				} {
					got, err := r.Messages(f.context.ID, f.admin.Session.ID, 1, 100, tc.f)
					if err != nil {
						return err
					}
					if len(got) != tc.count || tc.count != 0 && got[0].ID != m.ID {
						t.Fatalf("recovered filter %+v: %v", tc.f, got)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMessageProjectionTriggersCommitAtomically(t *testing.T) {
	f := setup(t, filepath.Join(t.TempDir(), "messages.db"))
	defer f.store.Close()
	ctx := context.Background()
	rollback := errors.New("rollback")
	err := f.store.Update(ctx, func(w model.WriteStore) error {
		q := TxQuerier(w.(*TxStore))
		if _, err := q.Exec("INSERT INTO messages VALUES(?,?,?,?,?,?,?)", "pending", f.context.ID, f.admin.Session.ID, 1, "pending", "ready", model.TS(*f.now)); err != nil {
			return err
		}
		checkHidden := func() error {
			return f.store.View(ctx, func(r model.ReadStore) error {
				for _, filter := range []model.MessageFilters{{}, {ForInstance: "reader"}, {ForInstance: "other"}, {FromInstance: "worker"}, {Schema: "comitia.discussion.v1"}} {
					got, err := r.Messages(f.context.ID, f.admin.Session.ID, 0, 100, filter)
					if err != nil {
						return err
					}
					if len(got) != 0 {
						t.Fatalf("uncommitted projections leaked: %+v", got)
					}
				}
				return nil
			})
		}
		// Provisional broadcast and final routing are invisible until commit.
		if err := checkHidden(); err != nil {
			return err
		}
		if _, err := q.Exec("INSERT INTO message_envelopes VALUES(?,?)", "pending", `{"schema":"comitia.discussion.v1","recipient_instances":["other"],"payload":{}}`); err != nil {
			return err
		}
		if _, err := q.Exec("INSERT INTO message_instances VALUES(?,?,?)", "pending", "worker", ""); err != nil {
			return err
		}
		if err := checkHidden(); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback: %v", err)
	}
	for _, table := range []string{"messages", "message_envelopes", "message_instances", "message_filters", "message_recipients", "comitia_message_repairs"} {
		var count int
		if err := DB(f.store).QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rollback left %s rows=%d: %v", table, count, err)
		}
	}
	if MigrationPending(f.store, ctx) {
		t.Fatal("rolled-back projections left migration work")
	}
}
