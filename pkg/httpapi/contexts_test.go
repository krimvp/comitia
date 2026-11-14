package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/sqlite"
)

func TestRemoteChildContexts(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.OpenSQLite(t.TempDir() + "/children.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "s"})
	if err != nil {
		t.Fatal(err)
	}
	_, admin, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "s", AdminName: "admin", SessionTTL: 4 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, admin.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	epic, err := svc.CreateContext(ctx, admin.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "epic"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewHandler(svc))
	defer ts.Close()
	c, err := NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	child, err := c.CreateChildContext(ctx, admin.Token, comitia.CreateChildContextInput{ParentContextID: epic.ID, Name: "child"})
	if err != nil || child.ParentContextID != epic.ID || child.WorkspaceID != ws.ID {
		t.Fatalf("child=%+v err=%v", child, err)
	}
	children, err := c.ListChildContexts(ctx, admin.Token, epic.ID, "", 10)
	if err != nil || len(children) != 1 || children[0] != child {
		t.Fatalf("children=%+v err=%v", children, err)
	}
	listed, err := c.ListContexts(ctx, admin.Token, "", 10)
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	_, reader, _, err := svc.DelegateSession(ctx, admin.Token, comitia.DelegateInput{PrincipalName: "r", TTL: time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: epic.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.CreateChildContext(ctx, reader.Token, comitia.CreateChildContextInput{ParentContextID: epic.ID, Name: "x"}); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("reader create: %v", err)
	}
	if _, err := c.ReplayMessages(ctx, reader.Token, child.ID, 0, 10); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("reader replay: %v", err)
	}
}
