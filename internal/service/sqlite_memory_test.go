package service

import (
	"context"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/sqlstore"
)

// database/sql replaces the working connection after a cancelled statement.
// An in-memory database must outlive that connection.
func TestMemoryStoreSurvivesConnectionReplacement(t *testing.T) {
	f := setup(t, ":memory:")
	ctx := context.Background()
	lookup := func() error {
		return f.store.View(ctx, func(r model.ReadStore) error {
			_, err := r.SessionByTokenHash(tokenHash(f.admin.Token))
			return err
		})
	}
	conn, err := sqlstore.DB(f.store).Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	_ = conn.Close()
	if err := lookup(); err != nil {
		t.Fatalf("lookup after the connection was replaced: %v", err)
	}
	// Writes cancelled at varying points must not lose earlier state either.
	for i := 0; i < 300; i++ {
		cancelled, cancel := context.WithTimeout(ctx, time.Duration(i%150)*time.Microsecond)
		_ = f.svc.SetInstanceResume(cancelled, f.admin.Token, f.context.ID, &model.AgentInstance{ID: "worker"}, []byte(`{}`))
		cancel()
		if err := lookup(); err != nil {
			t.Fatalf("lookup after cancelled write %d: %v", i, err)
		}
	}
	if _, err := f.svc.ListInstances(ctx, f.admin.Token, f.context.ID); err != nil {
		t.Fatal(err)
	}
}

// waitForQueryCancel is the context comitia_test_wait_cancel blocks on. Only
// TestSQLiteCancelledQueryReleasesConnection sets it; otherwise the function
// returns at once.
var waitForQueryCancel atomic.Pointer[context.Context]

func init() {
	// Registration is process-wide and applies to connections opened after it.
	// The name is unique to this test, and init runs once per test binary.
	sqlite.MustRegisterScalarFunction("comitia_test_wait_cancel", 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		if ctx := waitForQueryCancel.Load(); ctx != nil {
			<-(*ctx).Done()
			time.Sleep(time.Millisecond)
		}
		return int64(1), nil
	})
}

// A query whose context ends after its first row exists must not leave an
// unfinalized statement behind. modernc.org/sqlite before v1.40.1 dropped such
// rows unclosed; the discarded connection then lived on as a zombie holding its
// transaction, so later writes waited forever on a shared-cache memory database
// and failed with SQLITE_BUSY on a file database.
func TestSQLiteCancelledQueryReleasesConnection(t *testing.T) {
	for _, tc := range []struct{ name, path string }{
		{"memory", ":memory:"},
		{"file", filepath.Join(t.TempDir(), "c.db")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t, tc.path)
			defer f.store.Close()
			blockQuery := func(r model.ReadStore) error {
				rows, err := sqlstore.TxQuerier(r.(*sqlstore.TxStore)).Query("SELECT comitia_test_wait_cancel()")
				if err == nil {
					rows.Close()
				}
				return err
			}
			for _, op := range []string{"update", "view"} {
				ctx, cancel := context.WithCancel(context.Background())
				waitForQueryCancel.Store(&ctx)
				time.AfterFunc(10*time.Millisecond, cancel)
				var err error
				if op == "update" {
					err = f.store.Update(ctx, func(w model.WriteStore) error { return blockQuery(w) })
				} else {
					err = f.store.View(ctx, blockQuery)
				}
				waitForQueryCancel.Store(nil)
				cancel()
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled %s: %v", op, err)
				}
			}
			done := make(chan error, 1)
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				done <- f.svc.SetInstanceResume(ctx, f.admin.Token, f.context.ID, &model.AgentInstance{ID: "worker"}, []byte(`{}`))
			}()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("write after cancelled queries: %v", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("write after cancelled queries did not return")
			}
			if _, err := f.svc.ListInstances(context.Background(), f.admin.Token, f.context.ID); err != nil {
				t.Fatal(err)
			}
			if tc.path != ":memory:" {
				// A leaked statement keeps a read transaction open and blocks this.
				var busy, frames, done int
				if err := sqlstore.DB(f.store).QueryRow("PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &frames, &done); err != nil || busy != 0 {
					t.Fatalf("checkpoint after cancelled queries: busy=%d err=%v", busy, err)
				}
			}
		})
	}
}
