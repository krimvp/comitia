package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/sqlite"
)

func TestGrantRevokeCommandEmbeddedAndRemote(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded", true: "remote"}[remote], func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "revoke.db")
			store, err := sqlite.OpenSQLite(db)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "once"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			_, admin, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "once", AdminName: "owner", SessionTTL: 24 * time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			ws, err := svc.CreateWorkspace(ctx, admin.Token, "workspace")
			if err != nil {
				t.Fatal(err)
			}
			c, err := svc.CreateContext(ctx, admin.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "context"})
			if err != nil {
				t.Fatal(err)
			}
			_, issuer, source, err := svc.DelegateSession(ctx, admin.Token, comitia.DelegateInput{PrincipalName: "issuer", TTL: 8 * time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionRead | comitia.PermissionGrant, ExpiresAt: time.Now().Add(7 * time.Hour)}}})
			if err != nil {
				t.Fatal(err)
			}
			_, child, grants, err := svc.DelegateSession(ctx, issuer.Token, comitia.DelegateInput{PrincipalName: "child", TTL: 6 * time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(5 * time.Hour)}}})
			if err != nil {
				t.Fatal(err)
			}
			global := []string{"--db", db, "--credential", issuer.Token}
			if remote {
				server := httptest.NewServer(httpapi.NewHandler(svc))
				defer server.Close()
				global = []string{"--server-url", server.URL, "--credential", issuer.Token}
			}
			if err := commandErr(t, append(global, "grant-revoke")...); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("missing ID: %v", err)
			}
			if err := commandErr(t, append(global, "grant-revoke", "--grant-id", source[0].ID)...); !errors.Is(err, comitia.ErrForbidden) {
				t.Fatalf("other issuer: %v", err)
			}
			if err := commandErr(t, append(global, "grant-revoke", "--grant-id", "missing")...); !errors.Is(err, comitia.ErrForbidden) {
				t.Fatalf("unknown grant: %v", err)
			}
			got := command(t, append(global, "grant-revoke", "--grant-id", grants[0].ID)...)
			if got["ok"] != true {
				t.Fatalf("result=%v", got)
			}
			if _, err := svc.GetContext(ctx, child.Token, c.ID); !errors.Is(err, comitia.ErrForbidden) {
				t.Fatalf("revoked: %v", err)
			}
			global[3] = admin.Token
			command(t, append(global, "grant-revoke", "--grant-id", source[0].ID)...)
		})
	}
}
