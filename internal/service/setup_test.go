package service

import (
	"context"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/sqlstore"
)

// The fixture of service tests: a bootstrapped service on a SQLite store with
// an admin credential, one workspace and one context, at a fixed clock.
type fixture struct {
	svc     *Service
	store   *sqlstore.SQLiteStore
	admin   model.Credential
	context model.WorkContext
	now     *time.Time
}

func setup(t *testing.T, path string) fixture {
	t.Helper()
	st, e := sqlstore.OpenSQLite(path)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc, e := New(model.Config{Store: st, Clock: func() time.Time { return now }, BootstrapCredential: "local-secret"})
	if e != nil {
		t.Fatal(e)
	}
	_, admin, e := svc.Bootstrap(context.Background(), model.BootstrapInput{Credential: "local-secret", AdminName: "root", SessionTTL: 24 * time.Hour})
	if e != nil {
		t.Fatal(e)
	}
	ws, e := svc.CreateWorkspace(context.Background(), admin.Token, "test")
	if e != nil {
		t.Fatal(e)
	}
	wc, e := svc.CreateContext(context.Background(), admin.Token, model.CreateContextInput{WorkspaceID: ws.ID, Name: "main"})
	if e != nil {
		t.Fatal(e)
	}
	return fixture{svc: svc, store: st, admin: admin, context: wc, now: &now}
}
