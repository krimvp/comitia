package comitia

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

func TestListContextsScopePaginationAndRevocation(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	other, err := f.svc.CreateContext(ctx, f.admin.Token, CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	_, reader, grants, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{
		PrincipalName: "reader", TTL: time.Hour,
		Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := f.svc.ListContexts(ctx, reader.Token, "", 100)
	if err != nil || len(visible) != 1 || visible[0].ID != f.context.ID {
		t.Fatalf("scope: %+v %v", visible, err)
	}
	first, err := f.svc.ListContexts(ctx, f.admin.Token, "", 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := f.svc.ListContexts(ctx, f.admin.Token, first[0].ID, 1)
	if err != nil || len(second) != 1 || second[0].ID <= first[0].ID {
		t.Fatalf("second: %+v %v", second, err)
	}
	end, err := f.svc.ListContexts(ctx, f.admin.Token, second[0].ID, 1)
	if err != nil || len(end) != 0 {
		t.Fatalf("end: %+v %v", end, err)
	}
	if err := f.svc.RevokeGrant(ctx, f.admin.Token, grants[0].ID); err != nil {
		t.Fatal(err)
	}
	visible, err = f.svc.ListContexts(ctx, reader.Token, "", 100)
	if err != nil || len(visible) != 0 {
		t.Fatalf("revoked: %+v %v", visible, err)
	}
	// A newly minted admin session does not inherit another session's grants.
	admin, err := f.svc.MintSession(ctx, f.admin.Token, f.admin.Session.PrincipalID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	visible, err = f.svc.ListContexts(ctx, admin.Token, "", 100)
	if err != nil || len(visible) != 0 {
		t.Fatalf("admin bypass: %+v %v (other=%s)", visible, err, other.ID)
	}
	for _, limit := range []int{0, -1, 1001} {
		if _, err := f.svc.ListContexts(ctx, reader.Token, "", limit); !errors.Is(err, ErrInvalid) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	*f.now = f.now.Add(2 * time.Hour)
	if _, err := f.svc.ListContexts(ctx, reader.Token, "", 100); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired credential: %v", err)
	}
}

func TestListContextsSkipsUnreadableCandidatePages(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	// Deterministic IDs put > one candidate page before the readable context.
	err := f.store.Update(ctx, func(w WriteStore) error {
		for i := 0; i < 105; i++ {
			cid := fmt.Sprintf("!%031d", i)
			if err := w.PutContext(WorkContext{ID: cid, WorkspaceID: f.context.WorkspaceID, Name: "candidate", CreatedAt: *f.now}); err != nil {
				return err
			}
			if err := w.PutGrant(Grant{ID: model.NewID(), ContextID: cid, SessionID: f.admin.Session.ID, IssuerSessionID: f.admin.Session.ID, Permissions: PermissionWrite, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := f.svc.ListContexts(ctx, f.admin.Token, "", 1)
	if err != nil || len(visible) != 1 || visible[0].ID != f.context.ID {
		t.Fatalf("filtered pages: %+v %v", visible, err)
	}
}

func TestListContextsGrantExpiryDoesNotLeakContext(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	_, reader, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{
		PrincipalName: "observer", TTL: time.Hour,
		Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(30 * time.Minute)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(40 * time.Minute)
	visible, err := f.svc.ListContexts(ctx, reader.Token, "", 100)
	if err != nil || len(visible) != 0 {
		t.Fatalf("expired grant with live session: %+v %v", visible, err)
	}
}
