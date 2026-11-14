package comitia

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

// Each store has its own *sql.DB and therefore its own SQLite connection and
// file locks, like separate CLI processes sharing one database file.
func TestSQLiteConcurrentOpenAndWritesOnFreshFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.db")
	const stores, writes = 16, 20
	var start, done sync.WaitGroup
	start.Add(1)
	errs := make(chan error, stores*(writes+2))
	for i := 0; i < stores; i++ {
		done.Add(1)
		go func(i int) {
			defer done.Done()
			start.Wait()
			st, err := OpenSQLite(path)
			if err != nil {
				errs <- fmt.Errorf("open %d: %w", i, err)
				return
			}
			defer st.Close()
			for j := 0; j < writes; j++ {
				w := Workspace{ID: fmt.Sprintf("w%d-%d", i, j), Name: "n", CreatedAt: time.Now()}
				if err := st.Update(context.Background(), func(x WriteStore) error { return x.PutWorkspace(w) }); err != nil {
					errs <- fmt.Errorf("write %d/%d: %w", i, j, err)
				}
			}
		}(i)
	}
	start.Done()
	done.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var n int
	if err = st.db.QueryRow("SELECT COUNT(*) FROM workspaces").Scan(&n); err != nil || n != stores*writes {
		t.Fatalf("workspaces=%d err=%v", n, err)
	}
}

func TestSQLiteMigrationPendingOnlyWhenWorkRemains(t *testing.T) {
	f := setup(t, filepath.Join(t.TempDir(), "state.db"))
	defer f.store.Close()
	ctx := context.Background()
	coord, _ := delegate(t, f, f.admin.Token, "coord", f.context.ID, PermissionRead|PermissionWrite|PermissionCreate, time.Hour)
	if _, err := f.svc.CreateChildContext(ctx, coord.Token, CreateChildContextInput{ParentContextID: f.context.ID, Name: "child"}); err != nil {
		t.Fatal(err)
	}
	m, err := f.svc.AppendMessage(ctx, coord.Token, f.context.ID, MessageInput{DedupeKey: "k", Body: "b", AgentInstance: &AgentInstance{ID: "worker"}})
	if err != nil {
		t.Fatal(err)
	}
	if f.store.migrationPending(ctx) {
		t.Fatal("migrated store reports pending work")
	}
	// An older binary appends without a dedupe scope; the next open backfills it.
	if _, err = f.store.db.Exec("INSERT INTO messages VALUES(?,?,?,?,?,?,?)", "old", m.ContextID, m.SessionID, 2, "old-key", "old", model.TS(*f.now)); err != nil {
		t.Fatal(err)
	}
	if !f.store.migrationPending(ctx) {
		t.Fatal("unscoped message not detected")
	}
	if err = f.store.migrate(ctx); err != nil || f.store.migrationPending(ctx) {
		t.Fatalf("backfill: %v", err)
	}
}

// Opening a migrated database must not wait for the write lock: every CLI call
// opens the store, so a migration write per open starves writers under load.
func TestSQLiteOpenMigratedDatabaseWhileWriterHoldsLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	held, release, result := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		result <- st.Update(context.Background(), func(WriteStore) error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held
	started := time.Now()
	other, err := OpenSQLite(path)
	elapsed := time.Since(started)
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
	if err = <-result; err != nil {
		t.Fatal(err)
	}
	if elapsed > time.Second {
		t.Fatalf("open waited %v for the write lock", elapsed)
	}
}
