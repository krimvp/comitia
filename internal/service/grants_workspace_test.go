package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

// An admin ancestor's context grants must not establish workspace membership.
func TestGrantInviteRejectsAdminAncestorWorkspace(t *testing.T) {
	f := setup(t, ":memory:") // workspace A = f.context.WorkspaceID
	defer f.store.Close()
	ctx := context.Background()
	wsB, err := f.svc.CreateWorkspace(ctx, f.admin.Token, "B")
	if err != nil {
		t.Fatal(err)
	}
	cB, err := f.svc.CreateContext(ctx, f.admin.Token, model.CreateContextInput{WorkspaceID: wsB.ID, Name: "b"})
	if err != nil {
		t.Fatal(err)
	}
	// Grant holder in A only, delegated by admin.
	_, issuer, _, err := f.svc.DelegateSession(ctx, f.admin.Token, model.DelegateInput{PrincipalName: "a-coord", TTL: 8 * time.Hour, Grants: []model.DelegatedGrant{{ContextID: f.context.ID, Permissions: model.PermissionRead | model.PermissionGrant, ExpiresAt: f.now.Add(4 * time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	// Session that only belongs to workspace B, delegated by admin.
	_, bOnly, _, err := f.svc.DelegateSession(ctx, f.admin.Token, model.DelegateInput{PrincipalName: "b-agent", TTL: 8 * time.Hour, Grants: []model.DelegatedGrant{{ContextID: cB.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(4 * time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	g, err := f.svc.GrantContext(ctx, issuer.Token, model.GrantInput{SessionID: bOnly.Session.ID, ContextID: f.context.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(time.Hour)})
	if !errors.Is(err, model.ErrForbidden) || g.ID != "" {
		t.Fatalf("cross-workspace invite: %+v, %v; want forbidden", g, err)
	}
	if _, err = f.svc.GetContext(ctx, bOnly.Token, f.context.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("access after rejection: %v", err)
	}
	// Its own grant in A establishes eligibility even when it also belongs to B.
	otherA := inviteContext(t, f, f.context.WorkspaceID)
	inviteGrant(t, f, bOnly, otherA.ID, model.PermissionRead, 4*time.Hour)
	if _, err = f.svc.GrantContext(ctx, issuer.Token, model.GrantInput{SessionID: bOnly.Session.ID, ContextID: f.context.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
}

// Same with a non-admin coordinator holding grants in both A and B.
func TestGrantInviteRejectsCoordinatorAncestorWorkspace(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	wsB, err := f.svc.CreateWorkspace(ctx, f.admin.Token, "B")
	if err != nil {
		t.Fatal(err)
	}
	cB := inviteContext(t, f, wsB.ID)
	coord := inviteSession(t, f, "coord", 8*time.Hour)
	for _, cid := range []string{f.context.ID, cB.ID} {
		if _, err := f.svc.GrantContext(ctx, f.admin.Token, model.GrantInput{SessionID: coord.Session.ID, ContextID: cid, Permissions: model.PermissionRead | model.PermissionGrant, ExpiresAt: f.now.Add(4 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	_, bOnly, _, err := f.svc.DelegateSession(ctx, coord.Token, model.DelegateInput{PrincipalName: "b-agent", TTL: 4 * time.Hour, Grants: []model.DelegatedGrant{{ContextID: cB.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(3 * time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	issuer := inviteSession(t, f, "a-issuer", 8*time.Hour)
	if _, err := f.svc.GrantContext(ctx, f.admin.Token, model.GrantInput{SessionID: issuer.Session.ID, ContextID: f.context.ID, Permissions: model.PermissionRead | model.PermissionGrant, ExpiresAt: f.now.Add(4 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	g, err := f.svc.GrantContext(ctx, issuer.Token, model.GrantInput{SessionID: bOnly.Session.ID, ContextID: f.context.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(time.Hour)})
	if !errors.Is(err, model.ErrForbidden) || g.ID != "" {
		t.Fatalf("cross-workspace invite via coordinator: %+v, %v; want forbidden", g, err)
	}
	if _, err = f.svc.GetContext(ctx, bOnly.Token, f.context.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("access after rejection: %v", err)
	}
}
