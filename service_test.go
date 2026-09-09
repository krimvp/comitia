package comitia

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fixture struct {
	svc     *Service
	store   *SQLiteStore
	admin   Credential
	context WorkContext
	now     *time.Time
}

func setup(t *testing.T, path string) fixture {
	t.Helper()
	st, e := OpenSQLite(path)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc, e := New(Config{Store: st, Clock: func() time.Time { return now }, BootstrapCredential: "local-secret"})
	if e != nil {
		t.Fatal(e)
	}
	_, admin, e := svc.Bootstrap(context.Background(), BootstrapInput{Credential: "local-secret", AdminName: "root", SessionTTL: 24 * time.Hour})
	if e != nil {
		t.Fatal(e)
	}
	ws, e := svc.CreateWorkspace(context.Background(), admin.Token, "test")
	if e != nil {
		t.Fatal(e)
	}
	wc, e := svc.CreateContext(context.Background(), admin.Token, CreateContextInput{WorkspaceID: ws.ID, Name: "main"})
	if e != nil {
		t.Fatal(e)
	}
	return fixture{svc: svc, store: st, admin: admin, context: wc, now: &now}
}

func TestConcurrentMessageSequenceAndDedupe(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	const n = 32
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: fmt.Sprint(i), Body: "payload"})
			errs <- e
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	got, e := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	if len(got) != n {
		t.Fatalf("got %d", len(got))
	}
	for i, m := range got {
		if m.Sequence != int64(i+1) {
			t.Fatalf("sequence[%d]=%d", i, m.Sequence)
		}
	}
	same, e := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "0", Body: "payload"})
	if e != nil || same.DedupeKey != "0" {
		t.Fatalf("idempotency: %#v %v", same, e)
	}
	_, e = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "0", Body: "different"})
	if !errors.Is(e, ErrConflict) {
		t.Fatalf("want conflict, got %v", e)
	}
	if e = f.svc.AckMessages(ctx, f.admin.Token, f.context.ID, 3); e != nil {
		t.Fatal(e)
	}
	if cursor, e := f.svc.MessageCursor(ctx, f.admin.Token, f.context.ID); e != nil || cursor != 3 {
		t.Fatalf("cursor=%d err=%v", cursor, e)
	}
}

func TestSQLitePathForms(t *testing.T) {
	for _, path := range []string{filepath.Join(t.TempDir(), "absolute.db"), filepath.Join(".", "relative test +#?.db"), ":memory:"} {
		st, e := OpenSQLite(path)
		if e != nil {
			t.Fatalf("open %q: %v", path, e)
		}
		if e = st.Close(); e != nil {
			t.Fatalf("close %q: %v", path, e)
		}
		if path != ":memory:" {
			_ = removeDB(path)
		}
	}
}

func removeDB(path string) error { // SQLite may leave WAL sidecars after a clean close.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(path + suffix)
	}
	return nil
}

func TestLeaseFencingAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	f := setup(t, path)
	ctx := context.Background()
	task, e := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "work"})
	if e != nil {
		t.Fatal(e)
	}
	first, e := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Hour); !errors.Is(e, ErrConflict) {
		t.Fatalf("active reclaim: %v", e)
	}
	renewed, e := f.svc.RenewClaim(ctx, f.admin.Token, task.ID, first.Token, first.Task.LeaseVersion, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, first.Token, first.Task.LeaseVersion); !errors.Is(e, ErrStaleClaim) {
		t.Fatalf("old fence: %v", e)
	}
	f.store.Close()
	st, e := OpenSQLite(path)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	svc, _ := New(Config{Store: st, Clock: func() time.Time { return *f.now }})
	done, e := svc.CompleteTask(ctx, f.admin.Token, task.ID, renewed.Token, renewed.Task.LeaseVersion)
	if e != nil {
		t.Fatal(e)
	}
	if done.Status != TaskCompleted {
		t.Fatalf("status %s", done.Status)
	}
	if done.LeaseTokenHash != "" {
		t.Fatal("claim hash leaked")
	}
}

func TestDelegationCannotExpandAndRevocationCascades(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	p, e := f.svc.CreatePrincipal(ctx, f.admin.Token, "parent")
	if e != nil {
		t.Fatal(e)
	}
	parent, e := f.svc.MintSession(ctx, f.admin.Token, p.ID, 8*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	pg, e := f.svc.GrantContext(ctx, f.admin.Token, GrantInput{SessionID: parent.Session.ID, ContextID: f.context.ID, Permissions: PermissionRead | PermissionWrite | PermissionGrant, ExpiresAt: f.now.Add(4 * time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	_, child, cg, e := f.svc.DelegateSession(ctx, parent.Token, DelegateInput{PrincipalName: "child", TTL: 3 * time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(2 * time.Hour)}}})
	if e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = f.svc.DelegateSession(ctx, parent.Token, DelegateInput{PrincipalName: "bad", TTL: 3 * time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionAdmin, ExpiresAt: f.now.Add(time.Hour)}}}); !errors.Is(e, ErrForbidden) {
		t.Fatalf("expanded delegation: %v", e)
	}
	if _, e = f.svc.GetContext(ctx, child.Token, f.context.ID); e != nil {
		t.Fatal(e)
	}
	if e = f.svc.RevokeGrant(ctx, f.admin.Token, pg.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.GetContext(ctx, child.Token, f.context.ID); !errors.Is(e, ErrForbidden) {
		t.Fatalf("descendant grant remains: %v (%s)", e, cg[0].ID)
	}
}

func TestSessionRevocationInvalidatesGrantChain(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	_, parent, _, e := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{PrincipalName: "parent", TTL: 5 * time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead | PermissionGrant, ExpiresAt: f.now.Add(4 * time.Hour)}}})
	if e != nil {
		t.Fatal(e)
	}
	_, child, _, e := f.svc.DelegateSession(ctx, parent.Token, DelegateInput{PrincipalName: "child", TTL: 3 * time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(2 * time.Hour)}}})
	if e != nil {
		t.Fatal(e)
	}
	if e = f.svc.RevokeSession(ctx, f.admin.Token, parent.Session.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = f.svc.GetContext(ctx, child.Token, f.context.ID); !errors.Is(e, ErrUnauthenticated) {
		t.Fatalf("child session remains: %v", e)
	}
}

func TestListTasksRejectsInvalidStatus(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	if _, err := f.svc.ListTasks(context.Background(), f.admin.Token, f.context.ID, TaskStatus("unknown"), 100); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid task status accepted: %v", err)
	}
}
