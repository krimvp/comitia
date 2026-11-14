package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/sqlstore"
)

// Renewal-heavy fixture: one visible claim and several hidden renewals.
func renewalHistory(t *testing.T, f fixture, renewals int) model.Task {
	t.Helper()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < renewals; i++ {
		if c, err = f.svc.RenewClaim(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	return task
}

func storeMessageKinds(t *testing.T, st *sqlstore.SQLiteStore, f fixture, filters model.MessageFilters) []string {
	t.Helper()
	var kinds []string
	err := st.View(context.Background(), func(r model.ReadStore) error {
		messages, err := r.Messages(f.context.ID, f.admin.Session.ID, 0, 100, filters)
		for _, m := range messages {
			kind := "plain"
			if m.Envelope != nil && m.Envelope.Schema == model.TaskEventSchema {
				kind = "visible-event"
				if renewedMessage(m) {
					kind = "renewed"
				}
			}
			kinds = append(kinds, kind)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return kinds
}

func TestDefaultReadsSkipRenewalsByProjectionWithoutDecodingEnvelopes(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	renewalHistory(t, f, 5)
	for _, filters := range []model.MessageFilters{{}, {ForInstance: "reader"}} {
		if got := strings.Join(storeMessageKinds(t, f.store, f, filters), ","); got != "visible-event,visible-event" {
			t.Fatalf("default read %+v: %s", filters, got)
		}
		err := f.store.View(context.Background(), func(r model.ReadStore) error {
			q := &filterPlanQuery{Querier: sqlstore.TxQuerier(r.(*sqlstore.TxStore))}
			query, args := sqlstore.MessageQuery(f.context.ID, f.admin.Session.ID, 0, 100, filters)
			if strings.Contains(query, "json_extract") {
				t.Fatalf("default read decodes envelopes: %s", query)
			}
			rows, err := q.Query(query, args...)
			if err != nil {
				return err
			}
			rows.Close()
			if plan := strings.Join(q.plans, "\n"); !strings.Contains(plan, "SEARCH h USING PRIMARY KEY") {
				t.Fatalf("hidden skip is not an indexed probe:\n%s", plan)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := storeMessageKinds(t, f.store, f, model.MessageFilters{Schema: model.TaskEventSchema}); len(got) != 7 {
		t.Fatalf("explicit task-event read lost renewals: %v", got)
	}
}

func TestHiddenProjectionBackfillsOldDatabaseIncrementally(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hidden.db")
	f := setup(t, path)
	ctx := context.Background()
	task := renewalHistory(t, f, 3)
	// Model a database last written by a binary without the hidden projection.
	for _, query := range []string{"DROP TRIGGER comitia_message_hidden_insert", "DROP TABLE message_hidden"} {
		if _, err := sqlstore.DB(f.store).Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sqlstore.DB(f.store).Exec("DELETE FROM comitia_generations WHERE generation=?", sqlstore.SchemaGeneration(sqlstore.MessageHiddenSchemaSQL)); err != nil {
		t.Fatal(err)
	}
	// messageSchema rebuilds recipients; a surviving marker proves it did not rerun.
	if _, err := sqlstore.DB(f.store).Exec("INSERT INTO message_recipients SELECT id,context_id,seq,'marker','' FROM messages LIMIT 1"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := sqlstore.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { st.Close() }()
	if sqlstore.MigrationPending(st, ctx) {
		t.Fatal("hidden projection backfill still pending")
	}
	if got := strings.Join(storeMessageKinds(t, st, f, model.MessageFilters{}), ","); got != "visible-event,visible-event" {
		t.Fatalf("backfilled default read: %s", got)
	}
	var marker int
	if err = sqlstore.DB(st).QueryRow("SELECT COUNT(*) FROM message_recipients WHERE kind='marker'").Scan(&marker); err != nil || marker != 1 {
		t.Fatalf("hidden backfill reran the message projection rebuild: %d %v", marker, err)
	}
	// Rows inserted directly, as an older binary would, are still classified.
	envelope := `{"schema":"` + model.TaskEventSchema + `","task_id":"` + task.ID + `","payload":{"kind":"renewed","status":"leased","lease_version":9}}`
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO messages VALUES('legacy-renewal',?,?,100,'legacy-renewal','',?)", []any{f.context.ID, f.admin.Session.ID, model.TS(*f.now)}},
		{"INSERT INTO message_envelopes VALUES('legacy-renewal',?)", []any{envelope}},
	} {
		if _, err = sqlstore.DB(st).Exec(query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(storeMessageKinds(t, st, f, model.MessageFilters{}), ","); got != "visible-event,visible-event" {
		t.Fatalf("older writer's renewal shown by default: %s", got)
	}
}
