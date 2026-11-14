package comitia

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/sqlstore"
)

func holdSQLiteWriter(t *testing.T, st *SQLiteStore) func() {
	t.Helper()
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- st.Update(context.Background(), func(WriteStore) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	return func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}
}

func TestSQLiteWriterAdmissionCancellation(t *testing.T) {
	for _, separate := range []bool{false, true} {
		name := "local"
		if separate {
			name = "file"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			st, err := OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			writer := st
			if separate {
				writer, err = OpenSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
				defer writer.Close()
			}
			release := holdSQLiteWriter(t, writer)
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
			defer cancel()
			called := false
			done := make(chan error, 1)
			started := time.Now()
			go func() { done <- st.Update(ctx, func(WriteStore) error { called = true; return nil }) }()
			select {
			case err = <-done:
				release()
			case <-time.After(5 * time.Second):
				release()
				err = <-done
				t.Errorf("canceled admission waited for holder: %v", time.Since(started))
			}
			if !errors.Is(err, context.DeadlineExceeded) || called {
				t.Fatalf("err=%v callback=%v", err, called)
			}
			if err := st.Update(context.Background(), func(WriteStore) error { return nil }); err != nil {
				t.Fatalf("connection not reusable: %v", err)
			}
		})
	}
}

func TestSQLiteCallbackPanicReleasesTransaction(t *testing.T) {
	for _, update := range []bool{false, true} {
		name := "view"
		if update {
			name = "update"
		}
		t.Run(name, func(t *testing.T) {
			st, err := OpenSQLite(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			func() {
				defer func() {
					if recover() != "callback panic" {
						t.Fatal("panic not propagated")
					}
				}()
				if update {
					_ = st.Update(context.Background(), func(w WriteStore) error {
						if err := w.PutWorkspace(Workspace{ID: "uncommitted", Name: "panic", CreatedAt: time.Now()}); err != nil {
							t.Fatal(err)
						}
						panic("callback panic")
					})
				} else {
					_ = st.View(context.Background(), func(ReadStore) error { panic("callback panic") })
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := st.View(ctx, func(r ReadStore) error {
				_, err := r.Workspace("uncommitted")
				if !errors.Is(err, ErrNotFound) {
					return errors.New("panic transaction was committed")
				}
				return nil
			}); err != nil {
				t.Fatalf("connection stranded after panic: %v", err)
			}
			if err := st.Update(ctx, func(WriteStore) error { return nil }); err != nil {
				t.Fatalf("writer stranded after panic: %v", err)
			}
		})
	}
}

func TestReadPresenceBoundUnderHeldWriter(t *testing.T) {
	for _, state := range []string{"absent", "fresh", "due"} {
		for _, operation := range []string{"replay", "cursor", "wait-zero", "wait"} {
			t.Run(state+"/"+operation, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "state.db")
				f := setup(t, path)
				defer f.store.Close()
				ctx, raw, cid := context.Background(), f.admin.Token, f.context.ID
				m, err := f.svc.AppendMessage(ctx, raw, cid, MessageInput{DedupeKey: "ready", Body: "ready"})
				if err != nil {
					t.Fatal(err)
				}
				if state != "absent" {
					if _, err := f.svc.MessageCursor(ctx, raw, cid, "reader"); err != nil {
						t.Fatal(err)
					}
					if state == "due" {
						*f.now = f.now.Add(InstanceHeartbeatInterval)
					}
				}
				writer, err := OpenSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
				defer writer.Close()
				release := holdSQLiteWriter(t, writer)
				done := make(chan error, 1)
				go func() {
					var err error
					switch operation {
					case "replay":
						var rows []Message
						rows, err = f.svc.ReplayMessages(ctx, raw, cid, 0, 10, instanceFilter("reader"))
						if len(rows) != 1 {
							err = errors.New("read lost committed message")
						}
					case "cursor":
						_, err = f.svc.MessageCursor(ctx, raw, cid, "reader")
					default:
						timeout := time.Duration(0)
						if operation == "wait" {
							timeout = 60 * time.Millisecond
						}
						_, err = f.svc.WaitMessages(ctx, raw, cid, m.Sequence, timeout, instanceFilter("reader"))
					}
					done <- err
				}()
				select {
				case err = <-done:
					release()
				case <-time.After(5 * time.Second):
					release()
					err = <-done
					t.Error("optional heartbeat blocked read on writer")
				}
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

type overdueHeartbeatStore struct {
	Store
	t         *testing.T
	writer    *SQLiteStore
	delay     time.Duration
	scanCtx   context.Context
	heartbeat bool
}

func (s *overdueHeartbeatStore) View(ctx context.Context, fn func(ReadStore) error) error {
	if s.delay > 0 {
		timer := time.NewTimer(s.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	err := s.Store.View(ctx, fn)
	if err == nil {
		s.scanCtx = ctx
	}
	return err
}

func (s *overdueHeartbeatStore) Update(ctx context.Context, fn func(WriteStore) error) error {
	if s.scanCtx == nil {
		return errors.New("heartbeat before successful scan")
	}
	release := holdSQLiteWriter(s.t, s.writer)
	defer release()
	s.heartbeat = true
	// Model optional metadata work overrunning its budget, but only after the
	// initial scan has committed. Both the heartbeat and wait deadlines expire.
	<-s.scanCtx.Done()
	return s.Store.Update(ctx, fn)
}

func TestWaitInitialMatchSurvivesHeartbeatDeadline(t *testing.T) {
	for _, delay := range []time.Duration{0, 20 * time.Millisecond} {
		t.Run(delay.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			f := setup(t, path)
			defer f.store.Close()
			ctx, raw, cid := context.Background(), f.admin.Token, f.context.ID
			m, err := f.svc.AppendMessage(ctx, raw, cid, MessageInput{DedupeKey: "ready", Body: "ready"})
			if err != nil {
				t.Fatal(err)
			}
			writer, err := OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			store := &overdueHeartbeatStore{Store: f.store, t: t, writer: writer, delay: delay}
			f.svc.store = store
			started := time.Now()
			rows, err := f.svc.WaitMessages(ctx, raw, cid, 0, time.Second, instanceFilter("reader"))
			if store.scanCtx == nil || !store.heartbeat || !errors.Is(store.scanCtx.Err(), context.DeadlineExceeded) {
				t.Fatal("test did not expire the wait deadline after a successful scan")
			}
			if err != nil || len(rows) != 1 || rows[0].ID != m.ID {
				t.Fatalf("heartbeat expiry discarded successful initial match: %v %v", rows, err)
			}
			if elapsed := time.Since(started); elapsed > 2*time.Second {
				t.Fatalf("wait exceeded deadline bound: %v", elapsed)
			}
		})
	}
}

func TestWaitDeadlineBoundsScanAdmission(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	release := holdSQLiteWriter(t, f.store)
	done := make(chan error, 1)
	go func() {
		rows, err := f.svc.WaitMessages(context.Background(), f.admin.Token, f.context.ID, 0, 40*time.Millisecond)
		if err == nil && (rows == nil || len(rows) != 0) {
			err = errors.New("timeout must return an empty slice")
		}
		done <- err
	}()
	select {
	case err := <-done:
		release()
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		release()
		<-done
		t.Fatal("wait deadline did not bound initial scan admission")
	}
}

func TestPresenceLabelChangeBypassesThrottle(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx, raw, cid := context.Background(), f.admin.Token, f.context.ID
	if _, err := f.svc.MessageCursor(ctx, raw, cid, "worker"); err != nil {
		t.Fatal(err)
	}
	for i, label := range []string{"Worker", "Renamed"} {
		*f.now = f.now.Add(time.Second)
		if _, err := f.svc.AppendMessage(ctx, raw, cid, MessageInput{DedupeKey: label, Body: label, AgentInstance: &AgentInstance{ID: "worker", Label: label}}); err != nil {
			t.Fatal(err)
		}
		rows, err := f.svc.ListInstances(ctx, raw, cid)
		if err != nil || len(rows) != 1 || rows[0].Label != label || !rows[0].LastSeen.Equal(*f.now) {
			t.Fatalf("label change %d was throttled: %v %v", i, rows, err)
		}
	}
	seen := *f.now
	for _, label := range []string{"Renamed", ""} {
		*f.now = f.now.Add(time.Second)
		if _, err := f.svc.AppendMessage(ctx, raw, cid, MessageInput{DedupeKey: "unchanged-" + label, Body: "ready", AgentInstance: &AgentInstance{ID: "worker", Label: label}}); err != nil {
			t.Fatal(err)
		}
		rows, err := f.svc.ListInstances(ctx, raw, cid)
		if err != nil || len(rows) != 1 || rows[0].Label != "Renamed" || !rows[0].LastSeen.Equal(seen) {
			t.Fatalf("unchanged or empty label bypassed throttle: %v %v", rows, err)
		}
	}
}

func TestSQLiteBusyRetryCommitsAfterRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	writer, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	release := holdSQLiteWriter(t, writer)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	calls := 0
	go func() {
		done <- st.Update(ctx, func(w WriteStore) error {
			calls++
			return w.PutWorkspace(Workspace{ID: "retried", Name: "retry", CreatedAt: time.Now()})
		})
	}()
	select {
	case err := <-done:
		release()
		t.Fatalf("contended write returned before release: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil || calls != 1 {
		t.Fatalf("retry: err=%v callbacks=%d", err, calls)
	}
	if err := st.View(ctx, func(r ReadStore) error { _, err := r.Workspace("retried"); return err }); err != nil {
		t.Fatalf("retry not committed: %v", err)
	}
}

func TestSQLiteStatementCancellation(t *testing.T) {
	st, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	err = st.View(ctx, func(r ReadStore) error {
		var n int
		return sqlstore.TxQuerier(r.(*sqlstore.TxStore)).QueryRow(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<2000000) SELECT sum(x) FROM n`).Scan(&n)
	})
	if err == nil || ctx.Err() == nil || time.Since(started) > 250*time.Millisecond {
		t.Fatalf("statement ignored context: elapsed=%v err=%v ctx=%v", time.Since(started), err, ctx.Err())
	}
}
