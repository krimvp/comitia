package comitia

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestCheckPermissionsUsesEffectiveGrantsWithoutWriting(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	_, writer, grants, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{
		PrincipalName: "writer", TTL: time.Hour,
		Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionWrite, ExpiresAt: f.now.Add(30 * time.Minute)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.CheckPermissions(ctx, writer.Token, f.context.ID, PermissionWrite); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.CheckPermissions(ctx, writer.Token, f.context.ID, PermissionRead|PermissionWrite); !errors.Is(err, ErrForbidden) {
		t.Fatalf("missing read: %v", err)
	}
	if _, err = f.svc.GrantContext(ctx, f.admin.Token, GrantInput{SessionID: writer.Session.ID, ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.CheckPermissions(ctx, writer.Token, f.context.ID, PermissionRead|PermissionWrite); err != nil {
		t.Fatalf("separate effective grants: %v", err)
	}
	if err = f.svc.RevokeGrant(ctx, f.admin.Token, grants[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = f.svc.CheckPermissions(ctx, writer.Token, f.context.ID, PermissionRead|PermissionWrite); !errors.Is(err, ErrForbidden) {
		t.Fatalf("revoked write: %v", err)
	}
	if err = f.svc.CheckPermissions(ctx, "invalid", f.context.ID, PermissionRead); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("invalid token: %v", err)
	}
	for _, permission := range []Permission{0, PermissionAll << 6} {
		if err = f.svc.CheckPermissions(ctx, writer.Token, f.context.ID, permission); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid permissions %d: %v", permission, err)
		}
	}
	messages, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100)
	if err != nil || len(messages) != 0 {
		t.Fatalf("permission checks created messages: %v %v", messages, err)
	}
}
