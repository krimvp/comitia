package service

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

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
