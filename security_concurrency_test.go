package comitia

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// Authorization must be evaluated against the current durable state, even when
// callers use different Service/SQLiteStore instances for revoke and use.
func TestSecurityRevocationObservedAcrossStoreInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.db")
	f := setup(t, path)
	defer f.store.Close()
	ctx := context.Background()
	_, delegated, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{
		PrincipalName: "worker", TTL: time.Hour,
		Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(30 * time.Minute)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	otherStore, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer otherStore.Close()
	other, err := New(Config{Store: otherStore, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	if err = other.RevokeSession(ctx, f.admin.Token, delegated.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.GetContext(ctx, delegated.Token, f.context.ID); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("revoked session authorized through second store: %v", err)
	}
}

func TestSecurityOperationAfterConcurrentRevokeIsDenied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	f := setup(t, path)
	defer f.store.Close()
	ctx := context.Background()
	_, delegated, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{PrincipalName: "concurrent", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	revoker, err := New(Config{Store: st, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	revoked := make(chan error, 1)
	reads := make(chan error, 8)
	go func() {
		<-start
		revoked <- revoker.RevokeSession(ctx, f.admin.Token, delegated.Session.ID)
	}()
	for i := 0; i < cap(reads); i++ {
		go func() {
			<-start
			_, e := f.svc.GetContext(ctx, delegated.Token, f.context.ID)
			reads <- e
		}()
	}
	close(start)
	if err = <-revoked; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < cap(reads); i++ {
		if err = <-reads; err != nil && !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("non-linearizable racing read: %v", err)
		}
	}
	for i := 0; i < 8; i++ {
		if _, err = f.svc.GetContext(ctx, delegated.Token, f.context.ID); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("post-revoke operation authorized: %v", err)
		}
	}
}

func TestSecurityCrossContextTaskOwnershipAndListing(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	ws, err := f.svc.CreateWorkspace(ctx, f.admin.Token, "private")
	if err != nil {
		t.Fatal(err)
	}
	otherCtx, err := f.svc.CreateContext(ctx, f.admin.Token, CreateContextInput{WorkspaceID: ws.ID, Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "secret", Body: "payload"})
	if err != nil {
		t.Fatal(err)
	}
	_, outsider, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{
		PrincipalName: "outsider", TTL: time.Hour,
		Grants: []DelegatedGrant{{ContextID: otherCtx.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(30 * time.Minute)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.GetTask(ctx, outsider.Token, task.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-context task read leaked: %v", err)
	}
	if _, err = f.svc.ListTasks(ctx, outsider.Token, f.context.ID, TaskPending, 10); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-context task list leaked: %v", err)
	}
}

func TestSecurityCrossContextClaimAndStolenLeaseRejected(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	ws, err := f.svc.CreateWorkspace(ctx, f.admin.Token, "private")
	if err != nil {
		t.Fatal(err)
	}
	private, err := f.svc.CreateContext(ctx, f.admin.Token, CreateContextInput{WorkspaceID: ws.ID, Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "owned"})
	if err != nil {
		t.Fatal(err)
	}
	_, outsider, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{PrincipalName: "outsider", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: private.ID, Permissions: PermissionClaim, ExpiresAt: f.now.Add(time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ClaimTask(ctx, outsider.Token, task.ID, time.Minute); !errors.Is(err, ErrForbidden) {
		t.Fatalf("cross-context claim authorized: %v", err)
	}
	_, worker, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{PrincipalName: "worker", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionClaim, ExpiresAt: f.now.Add(time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.CompleteTask(ctx, worker.Token, task.ID, claim.Token, claim.Task.LeaseVersion); !errors.Is(err, ErrStaleClaim) {
		t.Fatalf("stolen lease token accepted: %v", err)
	}
}
