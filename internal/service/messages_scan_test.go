package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/sqlstore"

	"github.com/krimvp/comitia/internal/model"
)

type filterPlanQuery struct {
	sqlstore.Querier
	plans []string
}

func (q *filterPlanQuery) Query(query string, args ...any) (*sql.Rows, error) {
	rows, err := q.Querier.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			rows.Close()
			return nil, err
		}
		q.plans = append(q.plans, detail)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return q.Querier.Query(query, args...)
}
func TestFilteredMessagesSeekIndexes(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	for i := 0; i < 64; i++ {
		_, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: fmt.Sprint(i), Body: "skip", AgentInstance: &model.AgentInstance{ID: "other"}, Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{"other"}, Payload: json.RawMessage(`{}`)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, index string
		f           model.MessageFilters
	}{
		{"schema", "message_filters_schema", model.MessageFilters{Schema: "comitia.finding.v1"}},
		{"schemas", "message_filters_schema", model.MessageFilters{Schemas: []string{"comitia.finding.v1", "comitia.approval.v1"}}},
		{"schemas for instance", "message_filters_schema", model.MessageFilters{Schemas: []string{"comitia.finding.v1"}, ForInstance: "other"}},
		{"task", "message_filters_task", model.MessageFilters{TaskID: "missing"}},
		{"sender", "message_filters_sender", model.MessageFilters{FromInstance: "missing"}},
		{"recipient", "message_recipients_lookup", model.MessageFilters{ForInstance: "missing"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := f.store.View(ctx, func(r model.ReadStore) error {
				q := &filterPlanQuery{Querier: sqlstore.TxQuerier(r.(*sqlstore.TxStore))}
				messages, err := sqlstore.NewTxStore(q).Messages(f.context.ID, f.admin.Session.ID, 0, 100, tc.f)
				if err != nil {
					return err
				}
				if len(messages) != 0 {
					t.Fatalf("unexpected matches: %v", messages)
				}
				plan := strings.Join(q.plans, "\n")
				if !strings.Contains(plan, "SEARCH") || !strings.Contains(plan, tc.index) {
					t.Fatalf("no filter seek via %s:\n%s", tc.index, plan)
				}
				if strings.Contains(plan, "SCAN m") || strings.Contains(plan, "json_each") {
					t.Fatalf("history/JSON scan:\n%s", plan)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSchemaListFilterMatchesProjectionAndIsBounded(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	var ids []string
	for i, in := range []model.MessageInput{
		{Body: "plain"},
		{Body: "discussion", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", Payload: json.RawMessage(`{}`)}},
		{Body: "addressed", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{"other"}, Payload: json.RawMessage(`{}`)}},
	} {
		in.DedupeKey = fmt.Sprint(i)
		m, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	if _, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "event"}); err != nil {
		t.Fatal(err)
	}
	all, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100)
	if err != nil || len(all) != 4 {
		t.Fatalf("history: %v %v", all, err)
	}
	ids = append(ids, all[3].ID)
	for _, tc := range []struct {
		f    model.MessageFilters
		want []string
	}{
		{model.MessageFilters{Schemas: []string{model.TaskEventSchema, "comitia.discussion.v1"}}, ids[1:]},
		{model.MessageFilters{Schemas: []string{""}}, ids[:1]},
		{model.MessageFilters{Schemas: []string{"comitia.discussion.v1", model.TaskEventSchema}, Schema: model.TaskEventSchema}, ids[3:]},
		{model.MessageFilters{Schemas: []string{"comitia.discussion.v1"}, ForInstance: "reader"}, ids[1:2]},
		{model.MessageFilters{Schemas: []string{"comitia.discussion.v1"}, ForInstance: "other"}, ids[1:3]},
		{model.MessageFilters{Schemas: []string{"missing"}}, nil},
	} {
		for _, wait := range []bool{false, true} {
			var messages []model.Message
			if wait {
				messages, err = f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, 0, 0, tc.f)
			} else {
				messages, err = f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100, tc.f)
			}
			var got []string
			for _, m := range messages {
				got = append(got, m.ID)
			}
			if err != nil || strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("schemas %+v wait=%t: %v %v want %v", tc.f, wait, got, err, tc.want)
			}
		}
	}
	// Oversized lists are invalid input, not driver errors.
	full := make([]string, 64)
	for i := range full {
		full[i] = strings.Repeat("s", 127) + fmt.Sprint(i%10)
	}
	if _, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100, model.MessageFilters{Schemas: full}); err != nil {
		t.Fatalf("bounded list: %v", err)
	}
	for _, schemas := range [][]string{make([]string, 65), make([]string, 40000), {strings.Repeat("s", 129)}} {
		filter := model.MessageFilters{Schemas: schemas}
		if _, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 0, 100, filter); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("replay %d schemas: %v", len(schemas), err)
		}
		if _, err := f.svc.WaitMessagesPage(ctx, f.admin.Token, f.context.ID, 0, 0, filter); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("wait %d schemas: %v", len(schemas), err)
		}
		if err := f.store.View(ctx, func(r model.ReadStore) error {
			_, err := r.Messages(f.context.ID, f.admin.Session.ID, 0, 100, filter)
			return err
		}); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("store %d schemas: %v", len(schemas), err)
		}
	}
}

// Each recursion step must seek the frontier's replies; scanning every reply
// in the context per step makes thread reads quadratic in thread size.
func assertThreadPlan(t *testing.T, plans []string) {
	t.Helper()
	plan := strings.Join(plans, "\n")
	step := strings.Index(plan, "RECURSIVE STEP")
	if step < 0 || !strings.Contains(plan[step:], "SEARCH r USING INDEX message_replies_lookup (context_id=? AND reply_to=?)") || strings.Contains(plan, "SCAN r") {
		t.Fatalf("thread recursion does not seek replies by parent:\n%s", plan)
	}
}
func TestThreadFilterSeeksRepliesByParent(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	reply := func(key, to string) model.Message {
		t.Helper()
		m, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: key, Body: key, Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", ReplyTo: to, Payload: json.RawMessage(`{}`)}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	root := reply("root", "")
	reply("child", reply("parent", root.ID).ID)
	for _, filter := range []model.MessageFilters{{ThreadID: root.ID}, {ThreadID: root.ID, Schema: "comitia.discussion.v1"}, {ThreadID: root.ID, ForInstance: "reader"}} {
		err := f.store.View(ctx, func(r model.ReadStore) error {
			q := &filterPlanQuery{Querier: sqlstore.TxQuerier(r.(*sqlstore.TxStore))}
			messages, err := sqlstore.NewTxStore(q).Messages(f.context.ID, f.admin.Session.ID, 0, 100, filter)
			if err != nil {
				return err
			}
			if len(messages) != 3 {
				t.Fatalf("thread %+v: %v", filter, messages)
			}
			assertThreadPlan(t, q.plans)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

// Large star and chain threads keep the seeking plan and return only the
// tail; the plan, not wall-clock time, is the scaling contract.
func TestThreadFilterScalesWithThreadShape(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	const replies = 1000
	for _, shape := range []string{"star", "chain"} {
		t.Run(shape, func(t *testing.T) {
			root, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: shape, Body: shape})
			if err != nil {
				t.Fatal(err)
			}
			// Bulk-load replies; the insert triggers maintain message_replies.
			series := "WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<?) "
			if _, err = sqlstore.DB(f.store).Exec(series+"INSERT INTO messages SELECT ?||i,?,?,?+i,?||i,'reply',? FROM n", replies, shape, f.context.ID, f.admin.Session.ID, root.Sequence, shape, model.TS(*f.now)); err != nil {
				t.Fatal(err)
			}
			parent := "?2" // Star: every reply answers the root.
			if shape == "chain" {
				parent = "CASE i WHEN 1 THEN ?2 ELSE ?3||(i-1) END"
			}
			if _, err = sqlstore.DB(f.store).Exec(series+"INSERT INTO message_envelopes SELECT ?3||i,json_object('schema','comitia.discussion.v1','reply_to',"+parent+",'payload',json_object()) FROM n", replies, root.ID, shape); err != nil {
				t.Fatal(err)
			}
			tail := root.Sequence + replies
			page, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, tail-1, 10, model.MessageFilters{ThreadID: root.ID})
			if err != nil || len(page.Messages) != 1 || page.Messages[0].Sequence != tail || page.HasMore {
				t.Fatalf("tail page: %+v %v", page, err)
			}
			err = f.store.View(ctx, func(r model.ReadStore) error {
				q := &filterPlanQuery{Querier: sqlstore.TxQuerier(r.(*sqlstore.TxStore))}
				messages, err := sqlstore.NewTxStore(q).Messages(f.context.ID, f.admin.Session.ID, tail-1, 10, model.MessageFilters{ThreadID: root.ID})
				if err != nil || len(messages) != 1 {
					t.Fatalf("tail scan: %v %v", messages, err)
				}
				assertThreadPlan(t, q.plans)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func scanThrough(t *testing.T, page model.MessagePage) int64 {
	t.Helper()
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	var scanned int64
	if err = json.Unmarshal(fields["scanned_through"], &scanned); err != nil {
		t.Fatalf("missing scan metadata: %s (%v)", raw, err)
	}
	return scanned
}
func TestFilteredPageScanProgressWithoutAck(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: fmt.Sprint(i), Body: "skip"}); err != nil {
			t.Fatal(err)
		}
	}
	filter := model.MessageFilters{Schema: "comitia.finding.v1"}
	for _, wait := range []bool{false, true} {
		t.Run(fmt.Sprint("wait=", wait), func(t *testing.T) {
			var page model.MessagePage
			var err error
			if wait {
				page, err = f.svc.WaitMessagesPage(ctx, f.admin.Token, f.context.ID, 0, 0, filter)
			} else {
				page, err = f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 0, 100, filter)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Messages) != 0 || page.NextAfter != 0 || scanThrough(t, page) != 4 {
				t.Fatalf("empty scan: %+v", page)
			}
		})
	}
	if cursor, err := f.svc.MessageCursor(ctx, f.admin.Token, f.context.ID); err != nil || cursor != 0 {
		t.Fatalf("scan ack: %d %v", cursor, err)
	}
	page, err := f.svc.WaitMessagesPage(ctx, f.admin.Token, f.context.ID, 0, 60*time.Millisecond, filter)
	if err != nil || scanThrough(t, page) != 4 {
		t.Fatalf("timed wait scan: %+v %v", page, err)
	}
	// A scan cursor ahead of the log must never move backward.
	page, err = f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 100, 1, filter)
	if err != nil || scanThrough(t, page) != 100 {
		t.Fatalf("ahead: %+v %v", page, err)
	}
	// Count and byte truncation must preserve every omitted match.
	for _, filter := range []model.MessageFilters{{}, {MaxBytes: 1}} {
		limit := 1
		if filter.MaxBytes != 0 {
			limit = 100
		}
		page, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 0, limit, filter)
		if err != nil || len(page.Messages) != 1 || scanThrough(t, page) != 1 {
			t.Fatalf("truncated: %+v %v", page, err)
		}
	}
}

func TestMessageFilterBackfillAndLegacyRepair(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	f := setup(t, path)
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	inputs := []model.MessageInput{
		{Body: "broadcast"},
		{Body: "session", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", TaskID: task.ID, RecipientSessionIDs: []string{f.admin.Session.ID}, Payload: json.RawMessage(`{}`)}},
		{Body: "instance overrides session", AgentInstance: &model.AgentInstance{ID: "sender"}, Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientSessionIDs: []string{f.admin.Session.ID}, RecipientInstances: []string{"reader", "other"}, Payload: json.RawMessage(`{}`)}},
	}
	for i, in := range inputs {
		in.DedupeKey = fmt.Sprint(i)
		if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sqlstore.DB(f.store).Exec("DROP TABLE message_recipients; DROP TABLE message_filters"); err != nil {
		t.Fatal(err)
	}
	if !sqlstore.MigrationPending(f.store, ctx) {
		t.Fatal("missing filter tables not detected")
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := sqlstore.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { st.Close() }()
	assertReads := func() {
		t.Helper()
		if sqlstore.MigrationPending(st, ctx) {
			t.Fatal("complete projection backfill still pending")
		}
		if err := st.View(ctx, func(r model.ReadStore) error {
			for _, tc := range []struct {
				f    model.MessageFilters
				want string
			}{
				{model.MessageFilters{ForInstance: "other-reader"}, "broadcast,session"},
				{model.MessageFilters{ForInstance: "reader"}, "broadcast,session,instance overrides session"},
				{model.MessageFilters{FromInstance: "sender"}, "instance overrides session"},
				{model.MessageFilters{TaskID: task.ID}, "session"},
				{model.MessageFilters{Schema: "comitia.discussion.v1"}, "session,instance overrides session"},
			} {
				messages, err := r.Messages(f.context.ID, f.admin.Session.ID, 1, 100, tc.f)
				if err != nil {
					return err
				}
				var bodies []string
				for _, m := range messages {
					bodies = append(bodies, m.Body)
				}
				if strings.Join(bodies, ",") != tc.want {
					t.Fatalf("backfill filter %+v: %v want %s", tc.f, bodies, tc.want)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertReads()
	// A changed backfill generation backfills each missing projection class.
	for _, query := range []string{
		"DELETE FROM message_filters", "DELETE FROM message_recipients WHERE kind='broadcast'", "DELETE FROM message_recipients WHERE kind='session'", "DELETE FROM message_recipients WHERE kind='instance'",
	} {
		if _, err := sqlstore.DB(st).Exec(query); err != nil {
			t.Fatal(err)
		}
		for _, generation := range sqlstore.BackfillGenerations(sqlstore.Backfills[0]) {
			if _, err := sqlstore.DB(st).Exec("DELETE FROM comitia_generations WHERE generation=?", generation); err != nil {
				t.Fatal(err)
			}
		}
		if !sqlstore.MigrationPending(st, ctx) {
			t.Fatalf("new projection generation not detected: %s", query)
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
		st, err = sqlstore.OpenSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		assertReads()
	}
	// Model an older database generation before projection triggers existed.
	for _, trigger := range []string{"comitia_message_filter_insert", "comitia_message_envelope_filter_insert", "comitia_message_instance_filter_insert"} {
		if _, err := sqlstore.DB(st).Exec("DROP TRIGGER " + trigger); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sqlstore.DB(st).Exec("INSERT INTO messages VALUES(?,?,?,?,?,?,?)", "legacy", f.context.ID, f.admin.Session.ID, 5, "legacy", "legacy", model.TS(*f.now)); err != nil {
		t.Fatal(err)
	}
	if !sqlstore.MigrationPending(st, ctx) {
		t.Fatal("older writer was not detected")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = sqlstore.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if sqlstore.MigrationPending(st, ctx) {
		t.Fatal("legacy repair incomplete")
	}
	if err := st.View(ctx, func(r model.ReadStore) error {
		scan, ok := r.(model.MessageScanStore)
		if !ok {
			t.Fatal("SQLite scanner missing")
		}
		messages, watermark, err := scan.MessagesScanned(f.context.ID, f.admin.Session.ID, 4, 100, model.MessageFilters{ForInstance: "reader"})
		if err != nil {
			return err
		}
		if len(messages) != 1 || messages[0].ID != "legacy" || watermark != 5 {
			t.Fatalf("legacy repair: %v %d", messages, watermark)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type interleavedScanQuery struct {
	sqlstore.Querier
	append func()
}

func (q interleavedScanQuery) QueryRow(query string, args ...any) *sql.Row {
	if strings.Contains(query, "MAX(seq)") {
		q.append()
	}
	return q.Querier.QueryRow(query, args...)
}
func TestMessageScanWatermarkUsesReadSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	f := setup(t, path)
	defer f.store.Close()
	ctx := context.Background()
	if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "skip", Body: "skip"}); err != nil {
		t.Fatal(err)
	}
	// Use a deferred WAL read transaction so an independent append can commit
	// precisely between the empty result and the high-water query.
	dsn := (&url.URL{Scheme: "file", Path: path}).String() + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var appended model.Message
	q := interleavedScanQuery{sqlstore.TransactionQuerier(tx, ctx), func() {
		var err error
		appended, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "match", Body: "ready", AgentInstance: &model.AgentInstance{ID: "sender"}})
		if err != nil {
			t.Fatal(err)
		}
	}}
	messages, scanned, err := sqlstore.NewTxStore(q).MessagesScanned(f.context.ID, f.admin.Session.ID, 0, 100, model.MessageFilters{FromInstance: "sender"})
	if err != nil || len(messages) != 0 || scanned != 1 {
		t.Fatalf("mixed snapshots: %v %d %v", messages, scanned, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	page, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, scanned, 100, model.MessageFilters{FromInstance: "sender"})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != appended.ID {
		t.Fatalf("append skipped: %+v %v", page, err)
	}
}
