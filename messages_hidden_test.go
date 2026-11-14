package comitia

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

// Renewal-heavy fixture: one visible claim and several hidden renewals.
func renewalHistory(t *testing.T, f fixture, renewals int) Task {
	t.Helper()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "work"})
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

func storeMessageKinds(t *testing.T, st *SQLiteStore, f fixture, filters MessageFilters) []string {
	t.Helper()
	var kinds []string
	err := st.View(context.Background(), func(r ReadStore) error {
		messages, err := r.Messages(f.context.ID, f.admin.Session.ID, 0, 100, filters)
		for _, m := range messages {
			kind := "plain"
			if m.Envelope != nil && m.Envelope.Schema == TaskEventSchema {
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
	for _, filters := range []MessageFilters{{}, {ForInstance: "reader"}} {
		if got := strings.Join(storeMessageKinds(t, f.store, f, filters), ","); got != "visible-event,visible-event" {
			t.Fatalf("default read %+v: %s", filters, got)
		}
		err := f.store.View(context.Background(), func(r ReadStore) error {
			q := &filterPlanQuery{querier: r.(*sqlStore).q}
			query, args := messageQuery(f.context.ID, f.admin.Session.ID, 0, 100, filters)
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
	if got := storeMessageKinds(t, f.store, f, MessageFilters{Schema: TaskEventSchema}); len(got) != 7 {
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
		if _, err := f.store.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.store.db.Exec("DELETE FROM comitia_generations WHERE generation=?", schemaGeneration(messageHiddenSchema)); err != nil {
		t.Fatal(err)
	}
	// messageSchema rebuilds recipients; a surviving marker proves it did not rerun.
	if _, err := f.store.db.Exec("INSERT INTO message_recipients SELECT id,context_id,seq,'marker','' FROM messages LIMIT 1"); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { st.Close() }()
	if st.migrationPending(ctx) {
		t.Fatal("hidden projection backfill still pending")
	}
	if got := strings.Join(storeMessageKinds(t, st, f, MessageFilters{}), ","); got != "visible-event,visible-event" {
		t.Fatalf("backfilled default read: %s", got)
	}
	var marker int
	if err = st.db.QueryRow("SELECT COUNT(*) FROM message_recipients WHERE kind='marker'").Scan(&marker); err != nil || marker != 1 {
		t.Fatalf("hidden backfill reran the message projection rebuild: %d %v", marker, err)
	}
	// Rows inserted directly, as an older binary would, are still classified.
	envelope := `{"schema":"` + TaskEventSchema + `","task_id":"` + task.ID + `","payload":{"kind":"renewed","status":"leased","lease_version":9}}`
	for _, query := range []struct {
		sql  string
		args []any
	}{
		{"INSERT INTO messages VALUES('legacy-renewal',?,?,100,'legacy-renewal','',?)", []any{f.context.ID, f.admin.Session.ID, model.TS(*f.now)}},
		{"INSERT INTO message_envelopes VALUES('legacy-renewal',?)", []any{envelope}},
	} {
		if _, err = st.db.Exec(query.sql, query.args...); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(storeMessageKinds(t, st, f, MessageFilters{}), ","); got != "visible-event,visible-event" {
		t.Fatalf("older writer's renewal shown by default: %s", got)
	}
}
