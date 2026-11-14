package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/sqlstore"
)

// Original exported method sets compile without implementing the optional API.
type originalMessageAPI interface {
	ListParticipants(context.Context, string, string, int) ([]model.Participant, error)
	AppendMessage(context.Context, string, string, model.MessageInput) (model.Message, error)
	ReplayMessages(context.Context, string, string, int64, int, ...model.MessageFilters) ([]model.Message, error)
	AckMessages(context.Context, string, string, int64, ...string) error
	MessageCursor(context.Context, string, string, ...string) (int64, error)
	WaitMessages(context.Context, string, string, int64, time.Duration, ...model.MessageFilters) ([]model.Message, error)
}
type oldMessageAPI struct{ originalMessageAPI }
type oldMessageReadStore struct{ model.MessageReadStore }
type oldMessageWriteStore struct{ model.MessageWriteStore }

var _ model.MessageAPI = oldMessageAPI{}
var _ model.MessageReadStore = oldMessageReadStore{}
var _ model.MessageWriteStore = oldMessageWriteStore{}
var _ model.MessageQueryAPI = (*Service)(nil)
var _ model.MessageQueryStore = (*sqlstore.TxStore)(nil)

func TestScopedMessageQueries(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	other, err := f.svc.CreateContext(ctx, f.admin.Token, model.CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	appendTo := func(cid, key, reply string) model.Message {
		t.Helper()
		m, err := f.svc.AppendMessage(ctx, f.admin.Token, cid, model.MessageInput{DedupeKey: key, Body: key, Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", ReplyTo: reply, Payload: json.RawMessage(`{}`)}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	root := appendTo(f.context.ID, "root", "")
	first := appendTo(f.context.ID, "first", root.ID)
	nested := appendTo(f.context.ID, "nested", first.ID)
	last := appendTo(f.context.ID, "last", root.ID)
	foreign := appendTo(other.ID, "foreign", "")
	// Task events are attributed to the authorized actor but join no thread.
	if _, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "scoped"}); err != nil {
		t.Fatal(err)
	}
	events, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 10, model.MessageFilters{Schema: model.TaskEventSchema})
	if err != nil || len(events) != 1 {
		t.Fatalf("task event: %v %v", events, err)
	}
	if got, err := f.svc.GetMessage(ctx, f.admin.Token, f.context.ID, events[0].ID); err != nil || got.Envelope.Schema != model.TaskEventSchema {
		t.Fatalf("task event lookup: %+v %v", got, err)
	}
	got, err := f.svc.GetMessage(ctx, f.admin.Token, f.context.ID, root.ID)
	if err != nil || !reflect.DeepEqual(got, root) {
		t.Fatalf("lookup: %+v %v", got, err)
	}
	for _, mid := range []string{"missing", foreign.ID} {
		if _, err := f.svc.GetMessage(ctx, f.admin.Token, f.context.ID, mid); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("scope leak: %v", err)
		}
		if _, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 10, model.MessageFilters{ReplyTo: mid}); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("reply scope leak: %v", err)
		}
	}
	if _, err := f.svc.GetMessage(ctx, "invalid", f.context.ID, root.ID); !errors.Is(err, model.ErrUnauthenticated) {
		t.Fatal(err)
	}
	p, err := f.svc.CreatePrincipal(ctx, f.admin.Token, "ungranted")
	if err != nil {
		t.Fatal(err)
	}
	session, err := f.svc.MintSession(ctx, f.admin.Token, p.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.GetMessage(ctx, session.Token, f.context.ID, root.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("authorization: %v", err)
	}
	for _, tc := range []struct {
		filter model.MessageFilters
		ids    []string
	}{
		{model.MessageFilters{ReplyTo: root.ID}, []string{first.ID, last.ID}},
		{model.MessageFilters{ThreadID: root.ID}, []string{root.ID, first.ID, nested.ID, last.ID}},
		{model.MessageFilters{ThreadID: first.ID}, []string{first.ID, nested.ID}},
		{model.MessageFilters{ReplyTo: root.ID, ThreadID: root.ID, FromSession: f.admin.Session.ID, Schema: "comitia.discussion.v1", ForInstance: "reader", StrictRecipients: true}, []string{first.ID, last.ID}},
		{model.MessageFilters{ThreadID: root.ID, FromSession: "another"}, nil},
		{model.MessageFilters{FromSession: f.admin.Session.ID}, []string{root.ID, first.ID, nested.ID, last.ID, events[0].ID}},
		{model.MessageFilters{ReplyTo: events[0].ID}, nil},
		{model.MessageFilters{ThreadID: events[0].ID}, []string{events[0].ID}},
	} {
		for _, wait := range []bool{false, true} {
			var messages []model.Message
			if wait {
				messages, err = f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, 0, 0, tc.filter)
			} else {
				messages, err = f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100, tc.filter)
			}
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, m := range messages {
				ids = append(ids, m.ID)
			}
			if !reflect.DeepEqual(ids, tc.ids) {
				t.Fatalf("filter %+v wait=%v: %v", tc.filter, wait, ids)
			}
		}
	}
	page, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 0, 1, model.MessageFilters{ReplyTo: root.ID})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != first.ID || !page.HasMore {
		t.Fatalf("page: %+v %v", page, err)
	}
	next, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, page.ScannedThrough, 1, model.MessageFilters{ReplyTo: root.ID})
	if err != nil || len(next.Messages) != 1 || next.Messages[0].ID != last.ID {
		t.Fatalf("next: %+v %v", next, err)
	}
	if _, err := LookupMessage(ctx, oldMessageAPI{f.svc}, f.admin.Token, f.context.ID, root.ID); !errors.Is(err, model.ErrInvalid) {
		t.Fatal(err)
	}
	svc, err := New(model.Config{Store: noReceiptStore{f.store}, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 10, model.MessageFilters{ReplyTo: root.ID}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("old store widened: %v", err)
	}
}

func TestReplyProjectionRecoveryAndIndexedSeek(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.db")
	f := setup(t, path)
	defer func() { f.store.Close() }()
	ctx := context.Background()
	root, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "root", Body: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sqlstore.DB(f.store).Exec("DROP TRIGGER comitia_message_reply_insert"); err != nil {
		t.Fatal(err)
	}
	reply, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "reply", Body: "reply", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", ReplyTo: root.ID, Payload: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if !sqlstore.MigrationPending(f.store, ctx) {
		t.Fatal("missing reply trigger not detected")
	}
	f.store.Close()
	f.store, err = sqlstore.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if sqlstore.MigrationPending(f.store, ctx) {
		t.Fatal("schema still pending")
	}
	if err := f.store.View(ctx, func(r model.ReadStore) error {
		messages, err := r.Messages(f.context.ID, f.admin.Session.ID, 0, 10, model.MessageFilters{ReplyTo: root.ID})
		if err != nil || len(messages) != 1 || messages[0].ID != reply.ID {
			t.Fatalf("backfill: %v %v", messages, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, filter := range []model.MessageFilters{{ReplyTo: root.ID}, {ThreadID: root.ID}, {FromSession: f.admin.Session.ID}} {
		query, args := sqlstore.MessageQuery(f.context.ID, f.admin.Session.ID, 0, 10, filter)
		rows, err := sqlstore.DB(f.store).Query("EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		var details string
		for rows.Next() {
			var a, b, c int
			var d string
			if err := rows.Scan(&a, &b, &c, &d); err != nil {
				t.Fatal(err)
			}
			details += d + "\n"
		}
		rows.Close()
		want := "message_replies_lookup"
		if filter.FromSession != "" {
			want = "messages_author"
		}
		if !strings.Contains(details, want) {
			t.Fatalf("missing index %s: %s", want, details)
		}
	}
}

func TestScopedRecipientFiltersBeforeLimit(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	writer := messageWorker(t, f, "scoped-writer", f.context.ID)
	appendMessage := func(token, key, reply string, sessions, instances []string) model.Message {
		t.Helper()
		m, err := f.svc.AppendMessage(ctx, token, f.context.ID, model.MessageInput{DedupeKey: key, Body: key, Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", ReplyTo: reply, RecipientSessionIDs: sessions, RecipientInstances: instances, Payload: json.RawMessage(`{}`)}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	root := appendMessage(f.admin.Token, "root", "", nil, nil)
	routes := []struct {
		name                string
		sessions, instances []string
	}{
		{"broadcast", nil, nil},
		{"session", []string{f.admin.Session.ID}, nil},
		{"instance", nil, []string{"reader"}},
	}
	// Each recipient seek has more unrelated candidates than both limit=1 and
	// wait's default 100. No candidate class can mask another class's truncation.
	for _, route := range routes {
		for i := 0; i < 105; i++ {
			appendMessage(f.admin.Token, fmt.Sprintf("%s-%d", route.name, i), "", route.sessions, route.instances)
		}
	}
	mixed := appendMessage(writer.Token, "mixed", root.ID, []string{writer.Session.ID}, []string{"reader"})
	var eligible []model.Message
	for _, route := range routes {
		eligible = append(eligible, appendMessage(writer.Token, "reply-"+route.name, root.ID, route.sessions, route.instances))
	}
	for _, indexed := range []bool{false, true} {
		for _, strict := range []bool{false, true} {
			for _, kind := range []string{"reply", "thread", "author"} {
				t.Run(fmt.Sprintf("%s/indexed=%v/strict=%v", kind, indexed, strict), func(t *testing.T) {
					filter := model.MessageFilters{ForInstance: "reader", StrictRecipients: strict}
					switch kind {
					case "reply":
						filter.ReplyTo = root.ID
					case "thread":
						filter.ThreadID = root.ID
					case "author":
						filter.FromSession = writer.Session.ID
					}
					if indexed {
						filter.Schema = "comitia.discussion.v1"
					}
					want := eligible
					if !strict {
						want = append([]model.Message{mixed}, eligible...)
					}
					for _, limit := range []int{1, 100} {
						after := root.Sequence
						for i := 0; i < len(want); {
							page, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, after, limit, filter)
							count := min(limit, len(want)-i)
							if err != nil || len(page.Messages) != count {
								t.Fatalf("limit=%d after=%d: %+v %v; omitted matching seq=%d", limit, after, page, err, want[i].Sequence)
							}
							for j, m := range page.Messages {
								if m.ID != want[i+j].ID {
									t.Fatalf("wrong match: %s want %s", m.ID, want[i+j].ID)
								}
							}
							delivered := want[i+count-1].Sequence
							if page.NextAfter != delivered || page.ScannedThrough != delivered || page.HasMore != (i+count < len(want)) {
								t.Fatalf("limited page skipped an undelivered match: %+v", page)
							}
							// The documented durable-consumer recipe can safely ack this watermark.
							if err := f.svc.AckMessages(ctx, f.admin.Token, f.context.ID, page.ScannedThrough, "scoped-consumer"); err != nil {
								t.Fatal(err)
							}
							after = page.ScannedThrough
							i += count
						}
						empty, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, after, limit, filter)
						if err != nil || len(empty.Messages) != 0 || empty.HasMore || empty.ScannedThrough != eligible[len(eligible)-1].Sequence {
							t.Fatalf("end: %+v %v", empty, err)
						}
					}
					page, err := f.svc.WaitMessagesPage(ctx, f.admin.Token, f.context.ID, root.Sequence, 0, filter)
					if err != nil || len(page.Messages) != len(want) || page.Messages[0].ID != want[0].ID || page.ScannedThrough != want[len(want)-1].Sequence {
						t.Fatalf("wait skipped sparse matches: %+v %v", page, err)
					}
					// A byte-limited page must not report progress past an omitted reply.
					filter.MaxBytes = 1
					page, err = f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, root.Sequence, 100, filter)
					if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != want[0].ID || page.ScannedThrough != want[0].Sequence || !page.HasMore {
						t.Fatalf("byte limit: %+v %v", page, err)
					}
				})
			}
		}
	}
}
