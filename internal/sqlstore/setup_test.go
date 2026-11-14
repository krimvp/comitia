package sqlstore

import (
	"context"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/service"
	_ "modernc.org/sqlite"
)

// The fixture of store tests drives the store through a service, as the
// adapters do: a bootstrapped service with an admin credential, one workspace
// and one context, at a fixed clock. setup, delegate and childContext are
// copies of the service tests' helpers; test files cannot be shared between
// packages.
type fixture struct {
	svc     *service.Service
	store   *SQLiteStore
	admin   model.Credential
	context model.WorkContext
	now     *time.Time
}

func setup(t *testing.T, path string) fixture {
	t.Helper()
	st, e := OpenSQLite(path)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc, e := service.New(model.Config{Store: st, Clock: func() time.Time { return now }, BootstrapCredential: "local-secret"})
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

func delegate(t *testing.T, f fixture, parent, name, cid string, perms model.Permission, ttl time.Duration) (model.Credential, model.Grant) {
	t.Helper()
	_, c, gs, err := f.svc.DelegateSession(context.Background(), parent, model.DelegateInput{
		PrincipalName: name, TTL: ttl,
		Grants: []model.DelegatedGrant{{ContextID: cid, Permissions: perms, ExpiresAt: f.now.Add(ttl)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, gs[0]
}

func childContext(t *testing.T, f fixture, raw, parent string) model.WorkContext {
	t.Helper()
	c, err := f.svc.CreateChildContext(context.Background(), raw, model.CreateChildContextInput{ParentContextID: parent, Name: "child"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
