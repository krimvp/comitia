package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/sqlstore"

	"github.com/krimvp/comitia/internal/model"
)

var _ MessageReadOptionsAPI = (*Service)(nil)
var _ MessageReadOptionsStore = (*sqlstore.TxStore)(nil)

func TestExcludeSelfUsesSessionAndInstanceBeforeLimit(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	_, peer, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{PrincipalName: "peer", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead | PermissionWrite, ExpiresAt: f.now.Add(time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	appendMessage := func(token, key, instance string) Message {
		t.Helper()
		var agent *AgentInstance
		if instance != "" {
			agent = &AgentInstance{ID: instance}
		}
		m, err := f.svc.AppendMessage(ctx, token, f.context.ID, MessageInput{DedupeKey: key, Body: key, AgentInstance: agent})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	for i := 0; i < 5; i++ {
		appendMessage(f.admin.Token, fmt.Sprint(i), "reader")
	}
	otherInstance := appendMessage(f.admin.Token, "other-instance", "other")
	otherSession := appendMessage(peer.Token, "other-session", "reader")
	legacy := appendMessage(f.admin.Token, "legacy", "")
	instance := "reader"
	filter := MessageFilters{ExcludeSelf: true, AgentInstance: &instance, ForInstance: instance}
	page, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 0, 1, filter)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != otherInstance.ID || !page.HasMore {
		t.Fatalf("self before limit: %+v %v", page, err)
	}
	messages, err := f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, page.NextAfter, 0, filter)
	if err != nil || len(messages) != 2 || messages[0].ID != otherSession.ID || messages[1].ID != legacy.ID {
		t.Fatalf("session+instance: %v %v", messages, err)
	}
	page, err = f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 0, 100, MessageFilters{ExcludeSelf: true})
	if err != nil || len(page.Messages) != 7 || page.Messages[len(page.Messages)-1].ID == legacy.ID {
		t.Fatalf("legacy self scope: %+v %v", page, err)
	}
	page, err = f.svc.WaitMessagesPage(ctx, f.admin.Token, f.context.ID, 0, 0, MessageFilters{ExcludeSelf: true, AgentInstance: &instance, FromInstance: instance, FromSession: f.admin.Session.ID})
	if err != nil || len(page.Messages) != 0 || page.ScannedThrough != legacy.Sequence {
		t.Fatalf("all self skipped: %+v %v", page, err)
	}
	if cursor, err := f.svc.MessageCursor(ctx, f.admin.Token, f.context.ID, instance); err != nil || cursor != 0 {
		t.Fatalf("read acknowledged: %d %v", cursor, err)
	}
}

func TestCursorStartIsOptInAndInstanceScoped(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	first, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "first", Body: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AckMessages(ctx, f.admin.Token, f.context.ID, first.Sequence, "reader"); err != nil {
		t.Fatal(err)
	}
	instance := "reader"
	for _, wait := range []bool{false, true} {
		for _, fromCursor := range []bool{false, true} {
			filters := MessageFilters{FromCursor: fromCursor, AgentInstance: &instance}
			var page MessagePage
			if wait {
				page, err = f.svc.WaitMessagesPage(ctx, f.admin.Token, f.context.ID, 0, 0, filters)
			} else {
				page, err = f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 0, 100, filters)
			}
			want := 1
			if fromCursor {
				want = 0
			}
			if err != nil || len(page.Messages) != want || page.NextAfter != first.Sequence {
				t.Fatalf("wait=%t cursor=%t: %+v %v", wait, fromCursor, page, err)
			}
		}
	}
	if _, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 1, 100, MessageFilters{FromCursor: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ambiguous start: %v", err)
	}
	other := "other"
	messages, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100, MessageFilters{FromCursor: true, AgentInstance: &other})
	if err != nil || len(messages) != 1 {
		t.Fatalf("other cursor: %v %v", messages, err)
	}
	if _, err := f.svc.WaitMessages(ctx, "bad-token", f.context.ID, 0, 0, MessageFilters{FromCursor: true}); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unauthorized cursor: %v", err)
	}
}

func TestRenewedEventsSkippedWithScanProgress(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "lease"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		claim, err = f.svc.RenewClaim(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, filters := range []MessageFilters{{}, {ForInstance: "reader"}, {TaskID: task.ID}} {
		page, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 2, 1, filters)
		if err != nil || len(page.Messages) != 0 || page.NextAfter != 2 || page.ScannedThrough != 6 || page.HasMore {
			t.Fatalf("renewal-only replay %+v: %+v %v", filters, page, err)
		}
		page, err = f.svc.WaitMessagesPage(ctx, f.admin.Token, f.context.ID, 2, 300*time.Millisecond, filters)
		if err != nil || len(page.Messages) != 0 || page.ScannedThrough != 6 {
			t.Fatalf("renewal-only wait: %+v %v", page, err)
		}
	}
	for _, filters := range []MessageFilters{{Schema: TaskEventSchema}, {Schemas: []string{"comitia.discussion.v1", TaskEventSchema}}} {
		events, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100, filters)
		if err != nil || len(events) != 6 {
			t.Fatalf("explicit events: %d %v", len(events), err)
		}
		events, err = f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, 2, 0, filters)
		if err != nil || len(events) != 4 {
			t.Fatalf("explicit wait: %d %v", len(events), err)
		}
	}
	if _, err = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	for _, filters := range []MessageFilters{{}, {ForInstance: "reader"}} {
		page, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 2, 1, filters)
		if err != nil || len(page.Messages) != 1 || page.NextAfter != 7 {
			t.Fatalf("later event lost: %+v %v", page, err)
		}
		var payload TaskEventPayload
		if err = json.Unmarshal(page.Messages[0].Envelope.Payload, &payload); err != nil || payload.Kind != "completed" {
			t.Fatalf("terminal event: %+v %v", payload, err)
		}
	}
	messages, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100)
	if err != nil || len(messages) != 3 {
		t.Fatalf("other events hidden: %v %v", messages, err)
	}
}

func TestReadOptionsRejectLegacyCapabilities(t *testing.T) {
	for _, filters := range []MessageFilters{{ExcludeSelf: true}, {FromCursor: true}, {Schemas: []string{"comitia.discussion.v1"}}} {
		if err := CheckMessageQueryAPI(oldMessageAPI{}, filters); !errors.Is(err, ErrInvalid) {
			t.Fatalf("legacy API ignored %+v: %v", filters, err)
		}
	}
	f := setup(t, ":memory:")
	defer f.store.Close()
	if err := f.store.View(context.Background(), func(r ReadStore) error {
		return validateMessageQuery(legacyReadOptionsReader{r}, f.context.ID, MessageFilters{ExcludeSelf: true})
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("legacy store ignored self filter: %v", err)
	}
}

type legacyReadOptionsReader struct{ ReadStore }

// A scanner pauses after the initial snapshot, allowing another consumer to
// acknowledge a newly appended match while this wait is already in flight.
type cursorStartStore struct {
	Store
	scanned chan int64
	resume  chan struct{}
}
type cursorStartReader struct {
	ReadStore
	scanned chan int64
	resume  chan struct{}
}

func (s cursorStartStore) View(ctx context.Context, fn func(ReadStore) error) error {
	return s.Store.View(ctx, func(r ReadStore) error { return fn(cursorStartReader{r, s.scanned, s.resume}) })
}
func (r cursorStartReader) MessagesScanned(cid, sid string, after int64, limit int, f MessageFilters) ([]Message, int64, error) {
	messages, scanned, err := r.ReadStore.(MessageScanStore).MessagesScanned(cid, sid, after, limit, f)
	select {
	case r.scanned <- after:
		<-r.resume
	default:
	}
	return messages, scanned, err
}
func TestCursorStartResolvesOnceDuringWait(t *testing.T) {
	f := setup(t, filepath.Join(t.TempDir(), "cursor.db"))
	defer f.store.Close()
	ctx := context.Background()
	old, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "old", Body: "old"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.AckMessages(ctx, f.admin.Token, f.context.ID, old.Sequence, "reader"); err != nil {
		t.Fatal(err)
	}
	scanned := make(chan int64, 1)
	resume := make(chan struct{})
	svc, err := New(Config{Store: cursorStartStore{f.store, scanned, resume}, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	instance := "reader"
	result := make(chan []Message, 1)
	fail := make(chan error, 1)
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	go func() {
		messages, err := svc.WaitMessages(waitCtx, f.admin.Token, f.context.ID, 0, 10*time.Second, MessageFilters{FromCursor: true, AgentInstance: &instance})
		result <- messages
		fail <- err
	}()
	select {
	case after := <-scanned:
		if after != old.Sequence {
			t.Fatalf("initial scan: %d", after)
		}
	case <-waitCtx.Done():
		t.Fatal("initial scan stalled")
	}
	match, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "match", Body: "match"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.AckMessages(ctx, f.admin.Token, f.context.ID, match.Sequence, "reader"); err != nil {
		t.Fatal(err)
	}
	close(resume)
	messages := <-result
	err = <-fail
	if err != nil || len(messages) != 1 || messages[0].ID != match.ID {
		t.Fatalf("in-flight wait followed later ack: %v %v", messages, err)
	}
}

type legacyRenewalStore struct {
	Store
	scanner bool
}
type legacyRenewalReader struct{ ReadStore }
type legacyRenewalScanner struct{ legacyRenewalReader }

func (s legacyRenewalStore) View(ctx context.Context, fn func(ReadStore) error) error {
	return s.Store.View(ctx, func(r ReadStore) error {
		reader := legacyRenewalReader{r}
		if s.scanner {
			return fn(legacyRenewalScanner{reader})
		}
		return fn(reader)
	})
}
func (r legacyRenewalReader) Messages(cid, sid string, after int64, limit int, filters ...MessageFilters) ([]Message, error) {
	// Simulate an older store that returns renewals by default.
	f, err := model.CombineMessageFilters(filters)
	if err != nil {
		return nil, err
	}
	if f.Schema == "" {
		f.Schema = TaskEventSchema
	}
	return r.ReadStore.Messages(cid, sid, after, limit, f)
}
func (r legacyRenewalScanner) MessagesScanned(cid, sid string, after int64, limit int, f MessageFilters) ([]Message, int64, error) {
	messages, err := r.Messages(cid, sid, after, limit, f)
	if len(messages) > 0 {
		after = messages[len(messages)-1].Sequence
	}
	return messages, after, err
}
func TestDefaultRenewalSkipWithLegacyStores(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "lease"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		claim, err = f.svc.RenewClaim(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	for _, scanner := range []bool{false, true} {
		svc, err := New(Config{Store: legacyRenewalStore{f.store, scanner}, Clock: func() time.Time { return *f.now }})
		if err != nil {
			t.Fatal(err)
		}
		page, err := svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 2, 1)
		if err != nil || len(page.Messages) != 1 || page.Messages[0].Sequence != 6 {
			t.Fatalf("legacy scanner=%t lost later match: %+v %v", scanner, page, err)
		}
		events, err := svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 2, 100, MessageFilters{Schema: TaskEventSchema})
		if err != nil || len(events) != 4 {
			t.Fatalf("legacy scanner=%t explicit events: %d %v", scanner, len(events), err)
		}
	}
}

type readOptionsFallbackAPI struct{ MessageAPI }

func (readOptionsFallbackAPI) SupportsMessageReadOptions() bool { return true }
func TestCursorStartWithOptionalPagesFallback(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	m, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "m", Body: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.AckMessages(ctx, f.admin.Token, f.context.ID, m.Sequence, "reader"); err != nil {
		t.Fatal(err)
	}
	api := readOptionsFallbackAPI{f.svc}
	instance := "reader"
	filters := MessageFilters{FromCursor: true, AgentInstance: &instance}
	for _, wait := range []bool{false, true} {
		var page MessagePage
		if wait {
			page, err = WaitMessagePage(ctx, api, f.admin.Token, f.context.ID, 0, 0, filters)
		} else {
			page, err = ReplayMessagePage(ctx, api, f.admin.Token, f.context.ID, 0, 100, filters)
		}
		if err != nil || len(page.Messages) != 0 || page.NextAfter != m.Sequence {
			t.Fatalf("fallback wait=%t: %+v %v", wait, page, err)
		}
	}
}
