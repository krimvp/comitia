package comitia

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/sqlstore"
)

func TestSQLiteWALIndependentReadersAndWriter(t *testing.T) {
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	w := Workspace{ID: "before", Name: "before", CreatedAt: time.Now()}
	if err := st.Update(ctx, func(x WriteStore) error { return x.PutWorkspace(w) }); err != nil {
		t.Fatal(err)
	}
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- st.View(ctx, func(r ReadStore) error {
			if _, err := r.Workspace(w.ID); err != nil {
				return err
			}
			close(held)
			<-release
			// The writer committed while this snapshot was held.
			_, err := r.Workspace("after")
			if !errors.Is(err, ErrNotFound) {
				return errors.New("read snapshot changed after writer commit")
			}
			return nil
		})
	}()
	<-held
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := st.View(bounded, func(r ReadStore) error { _, err := r.Workspace(w.ID); return err }); err != nil {
		t.Fatalf("unrelated reader queued behind snapshot: %v", err)
	}
	w.ID = "after"
	if err := st.Update(bounded, func(x WriteStore) error { return x.PutWorkspace(w) }); err != nil {
		t.Fatalf("writer queued behind WAL reader: %v", err)
	}
	if err := st.View(bounded, func(r ReadStore) error { _, err := r.Workspace(w.ID); return err }); err != nil {
		t.Fatalf("new snapshot missed committed write: %v", err)
	}
}

func TestSQLiteWALReaderBoundAndCancellation(t *testing.T) {
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Four admitted snapshots fill the bounded pool; another must wait and
	// honor cancellation without running its callback.
	const readers = 4
	entered, release, done := make(chan struct{}, readers), make(chan struct{}), make(chan error, readers)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done <- st.View(context.Background(), func(ReadStore) error { entered <- struct{}{}; <-release; return nil })
		}()
	}
	defer func() {
		close(release)
		wg.Wait()
		for i := 0; i < readers; i++ {
			if err := <-done; err != nil {
				t.Error(err)
			}
		}
	}()
	for i := 0; i < readers; i++ {
		select {
		case <-entered:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d readers admitted", i)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	called := false
	if err := st.View(ctx, func(ReadStore) error { called = true; return nil }); !errors.Is(err, context.DeadlineExceeded) || called {
		t.Fatalf("reader admission: callback=%v err=%v", called, err)
	}
	// Reader saturation must not consume the writer connection.
	ctx, cancelWriter := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelWriter()
	if err := st.Update(ctx, func(WriteStore) error { return nil }); err != nil {
		t.Fatalf("readers consumed writer capacity: %v", err)
	}
}

func TestSQLiteMigrationProbeDoesNotReadHistoricalTables(t *testing.T) {
	for _, table := range []string{"messages", "context_parents"} {
		t.Run(table, func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			if _, err := f.svc.AppendMessage(context.Background(), f.admin.Token, f.context.ID, MessageInput{DedupeKey: "first", Body: "first"}); err != nil {
				t.Fatal(err)
			}
			childContext(t, f, f.admin.Token, f.context.ID)
			// A shared-cache table lock catches even a one-row historical scan,
			// without a history-sized fixture. Unchanged schema generations and
			// empty repair queues need no access to historical source tables.
			sqlstore.DB(f.store).SetMaxOpenConns(2)
			tx, err := sqlstore.DB(f.store).Begin()
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.Exec("UPDATE " + table + " SET " + map[string]string{"messages": "body=body", "context_parents": "parent_readable=parent_readable"}[table]); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			// Release even if a driver waits for shared-cache unlock notification.
			released := make(chan struct{})
			go func() {
				<-ctx.Done()
				tx.Rollback()
				close(released)
			}()
			pending := sqlstore.MigrationPending(f.store, ctx)
			cancel()
			<-released
			if pending {
				t.Fatal("routine migration probe touched locked historical " + table)
			}
		})
	}
}

func TestSQLiteWALReadersAreQueryOnly(t *testing.T) {
	st, err := OpenSQLite(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.View(context.Background(), func(r ReadStore) error {
		_, err := sqlstore.TxQuerier(r.(*sqlstore.TxStore)).Exec("INSERT INTO workspaces VALUES('bad','bad','bad')")
		if err == nil {
			return errors.New("read connection accepted a write")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(context.Background(), func(w WriteStore) error {
		return w.PutWorkspace(Workspace{ID: "good", Name: "good", CreatedAt: time.Now()})
	}); err != nil {
		t.Fatalf("query-only leaked to writer: %v", err)
	}
}

func TestSQLiteWALAuthorizationAndScanShareSnapshot(t *testing.T) {
	f := setup(t, filepath.Join(t.TempDir(), "state.db"))
	defer f.store.Close()
	ctx := context.Background()
	reader, grant := delegate(t, f, f.admin.Token, "reader", f.context.ID, PermissionRead, time.Hour)
	var committed Message
	if err := f.store.View(ctx, func(r ReadStore) error {
		grants, err := r.Grants(reader.Session.ID, f.context.ID, *f.now)
		if err != nil || len(grants) != 1 {
			return errors.New("initial read grant missing")
		}
		x := r.(*sqlstore.TxStore)
		messages, err := x.Messages(f.context.ID, reader.Session.ID, 0, 10)
		if err != nil || len(messages) != 0 {
			return errors.New("initial scan not empty")
		}
		bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		// Commit a revoke and append between the authorized scan and high
		// water read. Both must remain invisible to the existing snapshot.
		if err := f.store.Update(bounded, func(w WriteStore) error {
			if err := w.RevokeGrantTree(grant.ID, *f.now); err != nil {
				return err
			}
			committed, err = w.PutMessage(Message{ID: "new", ContextID: f.context.ID, SessionID: f.admin.Session.ID, DedupeKey: "new", Body: "new", CreatedAt: *f.now})
			return err
		}); err != nil {
			return err
		}
		grants, err = r.Grants(reader.Session.ID, f.context.ID, *f.now)
		if err != nil || len(grants) != 1 {
			return errors.New("authorization changed within read snapshot")
		}
		_, scanned, err := x.MessagesScanned(f.context.ID, reader.Session.ID, 0, 10, MessageFilters{FromInstance: "missing"})
		if err != nil || scanned != 0 {
			return errors.New("empty scan high-water skipped a concurrent append")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.View(ctx, func(r ReadStore) error {
		grants, err := r.Grants(reader.Session.ID, f.context.ID, *f.now)
		if err != nil || len(grants) != 0 {
			return errors.New("new snapshot missed revocation")
		}
		messages, err := r.Messages(f.context.ID, f.admin.Session.ID, 0, 10)
		if err != nil || len(messages) != 1 || messages[0].ID != committed.ID {
			return errors.New("new snapshot missed concurrent append")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
