package service

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"

	"github.com/krimvp/comitia/internal/sqlstore"
)

func TestReplayFiltersAndPaging(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	discussion := func(instances, sessions []string, task string) *model.MessageEnvelope {
		return &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: instances, RecipientSessionIDs: sessions, TaskID: task, Payload: json.RawMessage(`{}`)}
	}
	inputs := []model.MessageInput{
		{Body: "legacy"},
		{Body: "broadcast", Envelope: discussion(nil, nil, "")},
		{Body: "session only", Envelope: discussion(nil, []string{f.admin.Session.ID}, "")},
		{Body: "other instance", Envelope: discussion([]string{"alice-extra"}, nil, "")},
		{Body: "instance only", Envelope: discussion([]string{"alice"}, nil, "")},
		{Body: "both recipients", Envelope: discussion([]string{"alice"}, []string{f.admin.Session.ID}, "")},
		{Body: "task broadcast", Envelope: discussion(nil, nil, task.ID)},
		{Body: "finding", Envelope: &model.MessageEnvelope{Schema: "comitia.finding.v1", RecipientInstances: []string{"alice"}, Payload: json.RawMessage(`{"severity":"info","summary":"ok"}`)}},
		{Body: "finding broadcast", Envelope: &model.MessageEnvelope{Schema: "comitia.finding.v1", Payload: json.RawMessage(`{"severity":"info","summary":"ok"}`)}},
	}
	appended := taskEvents(t, f)
	for i, in := range inputs {
		in.DedupeKey = fmt.Sprint(i)
		if i > 0 {
			sender := "worker"
			if i >= 7 {
				sender = "worker-extra"
			}
			in.AgentInstance = &model.AgentInstance{ID: sender}
		}
		m, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		appended = append(appended, m)
	}
	unfiltered, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100)
	wantJSON, _ := json.Marshal(appended)
	gotJSON, _ := json.Marshal(unfiltered)
	if err != nil || !bytes.Equal(wantJSON, gotJSON) {
		t.Fatalf("unfiltered replay changed: %s %v", gotJSON, err)
	}
	tests := []struct {
		name   string
		filter model.MessageFilters
		want   []int64
	}{
		{"empty", model.MessageFilters{}, []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
		{"recipient", model.MessageFilters{ForInstance: "alice"}, []int64{1, 2, 3, 4, 6, 7, 8, 9, 10}},
		{"sender exact", model.MessageFilters{FromInstance: "worker"}, []int64{3, 4, 5, 6, 7, 8}},
		{"task", model.MessageFilters{TaskID: task.ID}, []int64{1, 8}},
		{"schema", model.MessageFilters{Schema: "comitia.discussion.v1"}, []int64{3, 4, 5, 6, 7, 8}},
		{"all filters", model.MessageFilters{ForInstance: "alice", FromInstance: "worker", TaskID: task.ID, Schema: "comitia.discussion.v1"}, []int64{8}},
		{"unknown task", model.MessageFilters{TaskID: "missing"}, nil},
		{"schema without matches", model.MessageFilters{Schema: "comitia.approval.v1"}, nil},
		{"unknown sender", model.MessageFilters{FromInstance: "work"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var seqs []int64
			var after int64
			for {
				page, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, after, 1, tc.filter)
				if err != nil {
					t.Fatal(err)
				}
				if len(page) == 0 {
					break
				}
				after = page[0].Sequence
				seqs = append(seqs, after)
				if len(seqs) > len(appended) {
					t.Fatal("cursor did not advance")
				}
			}
			if !reflect.DeepEqual(seqs, tc.want) {
				t.Fatalf("sequences %v, want %v", seqs, tc.want)
			}
		})
	}
	other, err := f.svc.CreateContext(ctx, f.admin.Token, model.CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	outsider := messageWorker(t, f, "outsider", other.ID)
	filter := model.MessageFilters{ForInstance: "alice"}
	if _, err := f.svc.ReplayMessages(ctx, outsider.Token, f.context.ID, 0, 100, filter); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("filtered replay visibility: %v", err)
	}
	if _, err := f.svc.WaitMessages(ctx, outsider.Token, f.context.ID, 0, time.Second, filter); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("wait visibility: %v", err)
	}
	for _, filters := range [][]model.MessageFilters{{{ForInstance: "bad instance"}}, {{FromInstance: "bad instance"}}, {{}, {}}} {
		if _, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100, filters...); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("invalid replay filter: %v", err)
		}
		if _, err := f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, 0, 0, filters...); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("invalid wait filter: %v", err)
		}
	}
}

type observingMessageStore struct {
	model.Store
	reads chan struct{}
}

func (s observingMessageStore) View(ctx context.Context, fn func(model.ReadStore) error) error {
	err := s.Store.View(ctx, fn)
	select {
	case s.reads <- struct{}{}:
	default:
	}
	return err
}

type waitResult struct {
	messages []model.Message
	err      error
}

type cancellingMessageStore struct {
	model.Store
	cancel context.CancelFunc
	err    error
}

func (s cancellingMessageStore) View(ctx context.Context, fn func(model.ReadStore) error) error {
	if err := s.Store.View(ctx, fn); err != nil {
		return err
	}
	s.cancel()
	return s.err
}

func TestWaitMessagesCancellationDuringRead(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	for _, readErr := range []error{errors.New("sql: transaction has already been committed or rolled back"), nil} {
		t.Run(fmt.Sprint(readErr), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reader, err := New(model.Config{Store: cancellingMessageStore{f.store, cancel, readErr}, Clock: f.svc.now})
			if err != nil {
				t.Fatal(err)
			}
			messages, err := reader.WaitMessages(ctx, f.admin.Token, f.context.ID, 0, 0)
			if !errors.Is(err, context.Canceled) || messages != nil {
				t.Fatalf("cancelled read: messages=%v error=%v", messages, err)
			}
		})
	}
}

type scanProbeStore struct {
	model.Store
	after chan int64
}

type scanProbeQuerier struct {
	sqlstore.Querier
	after int64
}

func (q *scanProbeQuerier) Query(query string, args ...any) (*sql.Rows, error) {
	if strings.HasPrefix(query, "SELECT m.id,") {
		q.after = args[1].(int64)
	}
	return q.Querier.Query(query, args...)
}

func (s scanProbeStore) View(ctx context.Context, fn func(model.ReadStore) error) error {
	var after int64
	err := s.Store.View(ctx, func(r model.ReadStore) error {
		q := &scanProbeQuerier{Querier: sqlstore.TxQuerier(r.(*sqlstore.TxStore))}
		err := fn(sqlstore.NewTxStore(q))
		after = q.after
		return err
	})
	s.after <- after
	return err
}

func TestWaitMessagesScansOnlyNewRows(t *testing.T) {
	testWaitMessagesScansOnlyNewRows(t, 0)
}

func TestWaitMessagesScansOnlyNewRowsWithStall(t *testing.T) {
	testWaitMessagesScansOnlyNewRows(t, 195*time.Millisecond)
}

func testWaitMessagesScansOnlyNewRows(t *testing.T, stall time.Duration) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wait.db")
	f := setup(t, path)
	defer f.store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	appendOther := func(svc *Service, key string) {
		t.Helper()
		_, err := svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: key, Body: "skip", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{"other"}, Payload: json.RawMessage(`{}`)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	const oldRows = 32
	for i := 0; i < oldRows; i++ {
		appendOther(f.svc, fmt.Sprint(i))
	}
	after := make(chan int64, 100)
	reader, err := New(model.Config{Store: scanProbeStore{f.store, after}, Clock: f.svc.now})
	if err != nil {
		t.Fatal(err)
	}
	st, err := sqlstore.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	writer, err := New(model.Config{Store: st, Clock: f.svc.now})
	if err != nil {
		t.Fatal(err)
	}
	result := startMessageWait(reader, ctx, f.admin.Token, f.context.ID, model.MessageFilters{ForInstance: "reader"})
	last := int64(-1)
	readAfter := func(want int64) {
		t.Helper()
		deadline := time.NewTimer(10 * time.Second)
		defer deadline.Stop()
		for {
			select {
			case got := <-after:
				if got < last || got > want || last >= 0 && got < oldRows {
					t.Fatalf("query after=%d, previous=%d, want advancement to %d", got, last, want)
				}
				last = got
				if got == want {
					return
				}
			case <-deadline.C:
				t.Fatalf("scan did not reach %d; last=%d", want, last)
			}
		}
	}
	readAfter(0)
	readAfter(oldRows)
	readAfter(oldRows)
	// This separate SQLite connection has no in-process notification.
	time.Sleep(stall)
	appendOther(writer, "new-other")
	readAfter(oldRows + 1)
	readAfter(oldRows + 1)
	m, err := writer.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "match", Body: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	got := receiveWait(t, result)
	if got.err != nil || len(got.messages) != 1 || got.messages[0].ID != m.ID {
		t.Fatalf("external append after skipped rows: %+v", got)
	}
	for len(after) != 0 {
		readAfter(oldRows + 1)
	}
	cursor, err := reader.MessageCursor(ctx, f.admin.Token, f.context.ID)
	if err != nil || cursor != 0 {
		t.Fatalf("wait changed ack cursor: %d %v", cursor, err)
	}
}

func TestWaitMessagesScanCursorAhead(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "old", Body: "old"}); err != nil {
		t.Fatal(err)
	}
	after := make(chan int64, 100)
	reader, err := New(model.Config{Store: scanProbeStore{f.store, after}, Clock: f.svc.now})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan waitResult, 1)
	go func() {
		messages, err := reader.WaitMessages(ctx, f.admin.Token, f.context.ID, 100, 30*time.Second)
		result <- waitResult{messages, err}
	}()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for i := 0; i < 3; i++ {
		select {
		case cursor := <-after:
			if cursor != 100 {
				t.Fatalf("cursor moved backwards: %d", cursor)
			}
		case <-deadline.C:
			t.Fatalf("only %d scans before deadline", i)
		}
	}
	cancel()
	if got := receiveWait(t, result); !errors.Is(got.err, context.Canceled) || len(got.messages) != 0 {
		t.Fatalf("cursor ahead: %+v", got)
	}
	for len(after) != 0 {
		if cursor := <-after; cursor != 100 {
			t.Fatalf("cursor moved backwards: %d", cursor)
		}
	}
}

func TestWaitMessagesMatchesReplayPages(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	for i := 0; i < 125; i++ {
		if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: fmt.Sprint(i), Body: "match", AgentInstance: &model.AgentInstance{ID: "sender"}}); err != nil {
			t.Fatal(err)
		}
	}
	filter := model.MessageFilters{FromInstance: "sender"}
	var after int64
	for _, count := range []int{100, 25} {
		want, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, after, 100, filter)
		if err != nil {
			t.Fatal(err)
		}
		got, err := f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, after, 0, filter)
		if err != nil || len(got) != count || !reflect.DeepEqual(got, want) {
			t.Fatalf("wait/replay mismatch: count=%d want=%d err=%v", len(got), count, err)
		}
		after = got[len(got)-1].Sequence
	}
}

func startMessageWait(svc *Service, ctx context.Context, token, cid string, filter model.MessageFilters) <-chan waitResult {
	result := make(chan waitResult, 1)
	go func() {
		messages, err := svc.WaitMessages(ctx, token, cid, 0, 30*time.Second, filter)
		result <- waitResult{messages, err}
	}()
	return result
}
func receiveWait(t *testing.T, result <-chan waitResult) waitResult {
	t.Helper()
	select {
	case got := <-result:
		return got
	case <-time.After(10 * time.Second):
		t.Fatal("wait did not return within ten seconds")
		return waitResult{}
	}
}

func TestWaitMessagesBroadcastAndPoll(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(fmt.Sprint("external=", external), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wait.db")
			f := setup(t, path)
			defer f.store.Close()
			reads := make(chan struct{}, 100)
			reader, err := New(model.Config{Store: observingMessageStore{f.store, reads}, Clock: f.svc.now})
			if err != nil {
				t.Fatal(err)
			}
			writer := reader
			if external {
				st, err := sqlstore.OpenSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				writer, err = New(model.Config{Store: st, Clock: f.svc.now})
				if err != nil {
					t.Fatal(err)
				}
			}
			const n = 8
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var waits []<-chan waitResult
			for i := 0; i < n; i++ {
				waits = append(waits, startMessageWait(reader, ctx, f.admin.Token, f.context.ID, model.MessageFilters{ForInstance: "reader"}))
			}
			for i := 0; i < n; i++ {
				select {
				case <-reads:
				case <-time.After(10 * time.Second):
					t.Fatal("initial read stalled")
				}
			}
			// A nonmatching append must wake readers without returning a message.
			if _, err := writer.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "other", Body: "skip", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{"other"}, Payload: json.RawMessage(`{}`)}}); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-waits[0]:
				t.Fatalf("returned nonmatching append: %+v", got)
			case <-time.After(30 * time.Millisecond):
			}
			start := time.Now()
			m, err := writer.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "match", Body: "ready"})
			if err != nil {
				t.Fatal(err)
			}
			for _, wait := range waits {
				got := receiveWait(t, wait)
				if got.err != nil || len(got.messages) != 1 || got.messages[0].ID != m.ID {
					t.Fatalf("wait result: %+v", got)
				}
			}
			// Woken by the poll, far inside the wait timeout; loose enough for a loaded race run.
			if elapsed := time.Since(start); elapsed >= 5*time.Second {
				t.Fatalf("append-to-return latency: %v", elapsed)
			}
		})
	}
}

func TestWaitTimeoutCancellationAndRevocation(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	start := time.Now()
	messages, err := f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, 0, 60*time.Millisecond)
	if err != nil || len(messages) != 0 || messages == nil || time.Since(start) < 60*time.Millisecond || time.Since(start) > time.Second {
		t.Fatalf("timeout: %v %v (%v)", messages, err, time.Since(start))
	}
	if _, err := f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, 0, -time.Second); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("negative timeout: %v", err)
	}
	if _, err := f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, -1, 0); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("negative cursor: %v", err)
	}
	for _, seconds := range []float64{-1, math.NaN(), math.Inf(1)} {
		if _, err := MessageWaitTimeout(seconds); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("invalid seconds %v: %v", seconds, err)
		}
	}
	if timeout, err := MessageWaitTimeout(math.MaxFloat64); err != nil || timeout != model.MaxMessageWait {
		t.Fatalf("timeout cap: %v %v", timeout, err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	result := startMessageWait(f.svc, cancelCtx, f.admin.Token, f.context.ID, model.MessageFilters{})
	cancel()
	if got := receiveWait(t, result); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancellation: %+v", got)
	}
	worker := messageWorker(t, f, "reader", f.context.ID)
	reads := make(chan struct{}, 1)
	reader, err := New(model.Config{Store: observingMessageStore{f.store, reads}, Clock: f.svc.now})
	if err != nil {
		t.Fatal(err)
	}
	result = startMessageWait(reader, ctx, worker.Token, f.context.ID, model.MessageFilters{})
	<-reads
	if err := f.svc.RevokeSession(ctx, f.admin.Token, worker.Session.ID); err != nil {
		t.Fatal(err)
	}
	if got := receiveWait(t, result); !errors.Is(got.err, model.ErrUnauthenticated) {
		t.Fatalf("revocation while waiting: %+v", got)
	}
	if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "ready", Body: "ready"}); err != nil {
		t.Fatal(err)
	}
	messages, err = f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, 0, 0)
	if err != nil || len(messages) != 1 {
		t.Fatalf("immediate wait: %v %v", messages, err)
	}
}

func TestInstanceReplayIncludesCallerSession(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	worker := messageWorker(t, f, "reader", f.context.ID)
	inputs := []model.MessageInput{
		{Body: "to owner session", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientSessionIDs: []string{f.admin.Session.ID}, Payload: json.RawMessage(`{}`)}},
		{Body: "to worker session", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientSessionIDs: []string{worker.Session.ID}, Payload: json.RawMessage(`{}`)}},
		{Body: "to both sessions", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientSessionIDs: []string{f.admin.Session.ID, worker.Session.ID}, Payload: json.RawMessage(`{}`)}},
		{Body: "to another owner instance", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientSessionIDs: []string{f.admin.Session.ID}, RecipientInstances: []string{"other"}, Payload: json.RawMessage(`{}`)}},
		{Body: "to reader instance", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{"reader"}, Payload: json.RawMessage(`{}`)}},
		{Body: "later broadcast"},
	}
	for i, in := range inputs {
		in.DedupeKey = fmt.Sprint(i)
		if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, token string
		want        []int64
	}{{"owner", f.admin.Token, []int64{1, 3, 5, 6}}, {"worker", worker.Token, []int64{2, 3, 5, 6}}} {
		t.Run(tc.name, func(t *testing.T) {
			for _, instance := range []string{"reader", "another"} {
				want := tc.want
				if instance == "another" {
					want = []int64{want[0], 3, 6}
				}
				filter := model.MessageFilters{ForInstance: instance}
				var got []int64
				var after int64
				for {
					page, err := f.svc.ReplayMessages(ctx, tc.token, f.context.ID, after, 1, filter)
					if err != nil {
						t.Fatal(err)
					}
					if len(page) == 0 {
						break
					}
					after = page[0].Sequence
					got = append(got, after)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s replay %v, want %v", instance, got, want)
				}
				messages, err := f.svc.WaitMessages(ctx, tc.token, f.context.ID, 0, time.Second, filter)
				if err != nil {
					t.Fatal(err)
				}
				got = nil
				for _, m := range messages {
					got = append(got, m.Sequence)
					if err := f.svc.AckMessages(ctx, tc.token, f.context.ID, m.Sequence, instance); err != nil {
						t.Fatal(err)
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s wait/ack skipped session message: %v, want %v", instance, got, want)
				}
				cursor, err := f.svc.MessageCursor(ctx, tc.token, f.context.ID, instance)
				if err != nil || cursor != 6 {
					t.Fatalf("ack cursor: %d %v", cursor, err)
				}
			}
		})
	}
}
