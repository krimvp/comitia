package comitia

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIssuerGrantRevocation(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	_, issuer, source, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{PrincipalName: "issuer", TTL: 8 * time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead | PermissionGrant, ExpiresAt: f.now.Add(7 * time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	_, child, childGrants, err := f.svc.DelegateSession(ctx, issuer.Token, DelegateInput{PrincipalName: "child", TTL: 6 * time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead | PermissionGrant, ExpiresAt: f.now.Add(5 * time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	_, grandchild, grandGrants, err := f.svc.DelegateSession(ctx, child.Token, DelegateInput{PrincipalName: "grandchild", TTL: 4 * time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(3 * time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	sibling := inviteSession(t, f, "sibling", 8*time.Hour)
	alternate, err := f.svc.MintSession(ctx, f.admin.Token, issuer.Session.PrincipalID, 8*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, token, grant string }{
		{"holder", issuer.Token, source[0].ID},
		{"recipient", child.Token, childGrants[0].ID},
		{"unrelated", sibling.Token, childGrants[0].ID},
		{"same principal different session", alternate.Token, childGrants[0].ID},
		{"ancestor issuer", issuer.Token, grandGrants[0].ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := f.svc.RevokeGrant(ctx, tc.token, tc.grant); !errors.Is(err, ErrForbidden) {
				t.Fatalf("revoke: %v", err)
			}
		})
	}
	if err := f.svc.RevokeGrant(ctx, issuer.Token, "missing"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing: %v", err)
	}
	if err := f.svc.RevokeGrant(ctx, "bad", childGrants[0].ID); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("bad credential: %v", err)
	}
	if err := f.svc.RevokeGrant(ctx, issuer.Token, childGrants[0].ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Credential{child, grandchild} {
		if _, err := f.svc.GetContext(ctx, c.Token, f.context.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("cascade: %v", err)
		}
	}
	if _, err := f.svc.GetContext(ctx, issuer.Token, f.context.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RevokeGrant(ctx, f.admin.Token, source[0].ID); err != nil {
		t.Fatal(err)
	}
}

func TestSessionRevocationOwnership(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	delegate := func(parent Credential, name string, ttl time.Duration) Credential {
		t.Helper()
		_, c, _, err := f.svc.DelegateSession(ctx, parent.Token, DelegateInput{PrincipalName: name, TTL: ttl, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead | PermissionGrant, ExpiresAt: f.now.Add(ttl - time.Minute)}}})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	issuer := delegate(f.admin, "issuer", 8*time.Hour)
	child := delegate(issuer, "child", 6*time.Hour)
	grandchild := delegate(child, "grandchild", 4*time.Hour)
	other := inviteSession(t, f, "other", 8*time.Hour)
	for _, tc := range []struct {
		caller Credential
		target string
	}{{issuer, issuer.Session.ID}, {other, child.Session.ID}, {issuer, grandchild.Session.ID}} {
		if err := f.svc.RevokeSession(ctx, tc.caller.Token, tc.target); !errors.Is(err, ErrForbidden) {
			t.Fatalf("ownership: %v", err)
		}
	}
	if err := f.svc.RevokeSession(ctx, issuer.Token, child.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.GetContext(ctx, grandchild.Token, f.context.ID); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("cascade: %v", err)
	}
	if _, err := f.svc.GetContext(ctx, issuer.Token, f.context.ID); err != nil {
		t.Fatal(err)
	}
}
