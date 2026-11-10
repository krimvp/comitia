package comitia

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingChangeStore keeps the SQLite change capability visible while
// counting completed authorized scans (Views) and change probes.
type countingChangeStore struct {
	*SQLiteStore
	views  atomic.Int64
	probes chan struct{}
}

func (s *countingChangeStore) View(ctx context.Context, fn func(ReadStore) error) error {
	defer s.views.Add(1)
	return s.SQLiteStore.View(ctx, fn)
}

func (s *countingChangeStore) ChangeVersion(ctx context.Context) (uint64, error) {
	v, err := s.SQLiteStore.ChangeVersion(ctx)
	s.probes <- struct{}{}
	return v, err
}

type manualClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *manualClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *manualClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func idleWaitFixture(t *testing.T, path string) (fixture, *countingChangeStore, *Service, *manualClock, chan time.Time) {
	t.Helper()
	f := setup(t, path)
	t.Cleanup(func() { f.store.Close() })
	clock := &manualClock{now: *f.now}
	store := &countingChangeStore{SQLiteStore: f.store, probes: make(chan struct{}, 1000)}
	reader, err := New(Config{Store: store, Clock: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	ticks := make(chan time.Time)
	reader.waitTicker = func() (<-chan time.Time, func()) { return ticks, func() {} }
	return f, store, reader, clock, ticks
}

func tick(t *testing.T, ticks chan<- time.Time) {
	t.Helper()
	select {
	case ticks <- time.Time{}:
	case <-time.After(10 * time.Second):
		t.Fatal("waiter stopped polling")
	}
}

// tickUntil polls until the wait returns.
func tickUntil(t *testing.T, ticks chan<- time.Time, result <-chan waitResult) waitResult {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case got := <-result:
			return got
		case ticks <- time.Time{}:
		case <-deadline:
			t.Fatal("wait did not return within ten seconds")
		}
	}
}

func awaitProbe(t *testing.T, store *countingChangeStore) {
	t.Helper()
	select {
	case <-store.probes:
	case <-time.After(10 * time.Second):
		t.Fatal("change probe did not run")
	}
}

func awaitViews(t *testing.T, store *countingChangeStore, want int64) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for store.views.Load() < want {
		if time.Now().After(deadline) {
			t.Fatalf("views=%d, want %d", store.views.Load(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestIdleWaitSkipsScansUntilStorageChanges(t *testing.T) {
	for _, path := range []string{":memory:", "file"} {
		t.Run(path, func(t *testing.T) {
			if path == "file" {
				path = filepath.Join(t.TempDir(), "idle.db")
			}
			f, store, reader, _, ticks := idleWaitFixture(t, path)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := startMessageWait(reader, ctx, f.admin.Token, f.context.ID, MessageFilters{})
			awaitProbe(t, store)
			awaitViews(t, store, 1)
			for i := 1; i < messageWaitReauthorizePolls; i++ {
				tick(t, ticks)
				awaitProbe(t, store)
				if got := store.views.Load(); got != 1 {
					t.Fatalf("idle poll %d ran %d scans", i, got)
				}
			}
			// The periodic reauthorization scan skips the probe comparison.
			tick(t, ticks)
			awaitProbe(t, store)
			awaitViews(t, store, 2)
			// A commit through f.svc reaches reader only via storage.
			m, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "idle", Body: "ready"})
			if err != nil {
				t.Fatal(err)
			}
			tick(t, ticks)
			got := receiveWait(t, result)
			if got.err != nil || len(got.messages) != 1 || got.messages[0].ID != m.ID {
				t.Fatalf("append after idle polls: %+v", got)
			}
		})
	}
}

func TestIdleWaitSeesOtherHandleAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idle.db")
	f, store, reader, _, ticks := idleWaitFixture(t, path)
	other, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	writer, err := New(Config{Store: other, Clock: f.svc.now})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := startMessageWait(reader, ctx, f.admin.Token, f.context.ID, MessageFilters{ForInstance: "reader"})
	awaitProbe(t, store)
	awaitViews(t, store, 1)
	if _, err := writer.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "skip", Body: "skip", Envelope: &MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{"other"}, Payload: []byte(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	tick(t, ticks)
	awaitViews(t, store, 2) // Nonmatching commits still rescan, without returning.
	m, err := writer.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "match", Body: "ready"})
	if err != nil {
		t.Fatal(err)
	}
	if got := tickUntil(t, ticks, result); got.err != nil || len(got.messages) != 1 || got.messages[0].ID != m.ID {
		t.Fatalf("other handle append: %+v", got)
	}
}

func TestIdleWaitReauthorizesPeriodically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idle.db")
	f, store, reader, clock, ticks := idleWaitFixture(t, path)
	worker := messageWorker(t, f, "reader", f.context.ID)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := startMessageWait(reader, ctx, worker.Token, f.context.ID, MessageFilters{})
	awaitViews(t, store, 1)
	// Grant expiry commits nothing, so only the periodic scan observes it.
	clock.Add(31 * time.Minute)
	for i := 0; i < messageWaitReauthorizePolls; i++ {
		select {
		case got := <-result:
			t.Fatalf("returned before reauthorization poll %d: %+v", i, got)
		case ticks <- time.Time{}:
		case <-time.After(10 * time.Second):
			t.Fatal("waiter stopped polling")
		}
	}
	if got := receiveWait(t, result); got.err == nil || !(errors.Is(got.err, ErrForbidden) || errors.Is(got.err, ErrUnauthenticated)) {
		t.Fatalf("expired grant while waiting: %+v", got)
	}
}

func TestIdleWaitRevocationByOtherHandle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idle.db")
	f, store, reader, _, ticks := idleWaitFixture(t, path)
	worker := messageWorker(t, f, "reader", f.context.ID)
	other, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	admin, err := New(Config{Store: other, Clock: f.svc.now})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := startMessageWait(reader, ctx, worker.Token, f.context.ID, MessageFilters{})
	awaitViews(t, store, 1)
	if err := admin.RevokeSession(ctx, f.admin.Token, worker.Session.ID); err != nil {
		t.Fatal(err)
	}
	// Polls well before the periodic reauthorization see the commit.
	if got := tickUntil(t, ticks, result); !errors.Is(got.err, ErrUnauthenticated) {
		t.Fatalf("revocation while waiting: %+v", got)
	}
}

func TestIdleWaitEndsPromptly(t *testing.T) {
	f, store, reader, _, _ := idleWaitFixture(t, ":memory:")
	ctx, cancel := context.WithCancel(context.Background())
	result := startMessageWait(reader, ctx, f.admin.Token, f.context.ID, MessageFilters{})
	awaitViews(t, store, 1)
	cancel()
	if got := receiveWait(t, result); !errors.Is(got.err, context.Canceled) {
		t.Fatalf("cancellation: %+v", got)
	}
	result = startMessageWait(reader, context.Background(), f.admin.Token, f.context.ID, MessageFilters{})
	awaitViews(t, store, 2)
	reader.EndWaits()
	if got := receiveWait(t, result); got.err != nil || got.messages == nil || len(got.messages) != 0 {
		t.Fatalf("ended wait: %+v", got)
	}
}
