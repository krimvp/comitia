package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/httpapi"
	"github.com/krimvp/comitia/pkg/sqlite"
)

func TestGrantCommandEmbeddedAndRemote(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded", true: "remote"}[remote], func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "grants.db")
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
			_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "once", AdminName: "owner", SessionTTL: 24 * time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			ws, err := svc.CreateWorkspace(ctx, owner.Token, "workspace")
			if err != nil {
				t.Fatal(err)
			}
			c, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "destination"})
			if err != nil {
				t.Fatal(err)
			}
			other, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "existing"})
			if err != nil {
				t.Fatal(err)
			}
			_, issuer, grants, err := svc.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "issuer", TTL: 4 * time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionRead | comitia.PermissionGrant, ExpiresAt: time.Now().Add(3 * time.Hour)}}})
			if err != nil {
				t.Fatal(err)
			}
			_, target, targetGrants, err := svc.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "target", TTL: 4 * time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: other.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(3 * time.Hour)}}})
			if err != nil {
				t.Fatal(err)
			}
			global := []string{"--db", db, "--credential", issuer.Token}
			if remote {
				server := httptest.NewServer(httpapi.NewHandler(svc))
				defer server.Close()
				global = []string{"--server-url", server.URL, "--credential", issuer.Token}
			}
			args := []string{"grant", "--session-id", target.Session.ID, "--context-id", c.ID, "--permissions", "read", "--ttl", "30m"}
			outsideWS, err := svc.CreateWorkspace(ctx, owner.Token, "outside")
			if err != nil {
				t.Fatal(err)
			}
			outside, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: outsideWS.ID, Name: "outside"})
			if err != nil {
				t.Fatal(err)
			}
			_, outsider, _, err := svc.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "outsider", TTL: 4 * time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: outside.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(3 * time.Hour)}}})
			if err != nil {
				t.Fatal(err)
			}
			crossWorkspace := append([]string(nil), args...)
			crossWorkspace[2] = outsider.Session.ID
			if err = commandErr(t, append(global, crossWorkspace...)...); !errors.Is(err, comitia.ErrForbidden) {
				t.Fatalf("cross-workspace invite: %v", err)
			}
			// The owner's grants cannot replace the target's own membership grant.
			if err = svc.RevokeGrant(ctx, owner.Token, targetGrants[0].ID); err != nil {
				t.Fatal(err)
			}
			if err = commandErr(t, append(global, args...)...); !errors.Is(err, comitia.ErrForbidden) {
				t.Fatalf("invite without own membership: %v", err)
			}
			if _, err = svc.GrantContext(ctx, owner.Token, comitia.GrantInput{SessionID: target.Session.ID, ContextID: other.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(3 * time.Hour)}); err != nil {
				t.Fatal(err)
			}
			before := time.Now()
			got := command(t, append(global, args...)...)
			exp, err := time.Parse(time.RFC3339Nano, got["ExpiresAt"].(string))
			if err != nil {
				t.Fatal(err)
			}
			if got["ParentGrantID"] != grants[0].ID || got["IssuerSessionID"] != issuer.Session.ID || got["SessionID"] != target.Session.ID || got["ContextID"] != c.ID || got["Permissions"] != float64(comitia.PermissionRead) || exp.Before(before.Add(30*time.Minute)) || exp.After(time.Now().Add(30*time.Minute)) {
				t.Fatalf("grant=%v", got)
			}
			if _, err = svc.GetContext(ctx, target.Token, c.ID); err != nil {
				t.Fatal(err)
			}
			for _, bad := range [][]string{
				{"grant"}, {"grant", "--session-id", target.Session.ID},
				{"grant", "--session-id", target.Session.ID, "--context-id", c.ID, "--ttl", "0"},
				{"grant", "--session-id", target.Session.ID, "--context-id", c.ID, "--ttl", "-1h"},
				{"grant", "--session-id", target.Session.ID, "--context-id", c.ID, "--ttl", "bad"},
				{"grant", "--session-id", target.Session.ID, "--context-id", c.ID, "--permissions", "unknown"},
			} {
				if err := commandErr(t, append(global, bad...)...); err == nil {
					t.Fatalf("accepted %v", bad)
				}
			}
			bad := append([]string(nil), args...)
			bad[6] = "write"
			if err := commandErr(t, append(global, bad...)...); !errors.Is(err, comitia.ErrForbidden) {
				t.Fatalf("widen: %v", err)
			}
			if err = svc.RevokeGrant(ctx, owner.Token, grants[0].ID); err != nil {
				t.Fatal(err)
			}
			if _, err = svc.GetContext(ctx, target.Token, c.ID); !errors.Is(err, comitia.ErrForbidden) {
				t.Fatalf("cascade: %v", err)
			}
		})
	}
}
