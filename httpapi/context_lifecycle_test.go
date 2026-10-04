package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

func TestRemoteContextLimitsAndArchive(t *testing.T) {
	ctx := context.Background()
	store, err := comitia.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "s", MaxContextDepth: 1, MaxChildContextsPerGrant: 1})
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
	root, err := svc.CreateContext(ctx, admin.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "root"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewHandler(svc))
	defer ts.Close()
	c, err := NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	child, err := c.CreateChildContext(ctx, admin.Token, comitia.CreateChildContextInput{ParentContextID: root.ID, Name: "child"})
	if err != nil {
		t.Fatal(err)
	}
	for parent, detail := range map[string]string{root.ID: "per creating grant", child.ID: "nesting depth"} {
		if _, err := c.CreateChildContext(ctx, admin.Token, comitia.CreateChildContextInput{ParentContextID: parent, Name: "over limit"}); !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), detail) {
			t.Fatalf("limit error: %v", err)
		}
	}
	_, reader, _, err := svc.DelegateSession(ctx, admin.Token, comitia.DelegateInput{PrincipalName: "reader", TTL: time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: child.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(30 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ArchiveContext(ctx, reader.Token, child.ID); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("reader archive: %v", err)
	}
	message, err := c.AppendMessage(ctx, admin.Token, child.ID, comitia.MessageInput{DedupeKey: "k", Body: "saved"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.ArchiveContext(ctx, admin.Token, child.ID); err != nil {
		t.Fatal(err)
	}
	if err := c.ArchiveContext(ctx, admin.Token, child.ID); err != nil {
		t.Fatal(err)
	}
	got, err := c.GetContext(ctx, reader.Token, child.ID)
	if err != nil || !got.Archived || got.ArchivedAt == nil {
		t.Fatalf("metadata: %+v %v", got, err)
	}
	children, err := c.ListChildContexts(ctx, admin.Token, root.ID, "", 100)
	if err != nil || len(children) != 1 || !children[0].Archived {
		t.Fatalf("children: %+v %v", children, err)
	}
	if _, err := c.AppendMessage(ctx, admin.Token, child.ID, comitia.MessageInput{DedupeKey: "new", Body: "x"}); !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "archived") {
		t.Fatalf("write error: %v", err)
	}
	ms, err := c.ReplayMessages(ctx, reader.Token, child.ID, 0, 100)
	if err != nil || len(ms) != 1 || ms[0].ID != message.ID {
		t.Fatalf("replay: %+v %v", ms, err)
	}
}
