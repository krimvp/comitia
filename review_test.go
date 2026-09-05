package comitia

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type reviewFixture struct {
	t     *testing.T
	s     *Service
	db    *SQLiteStore
	path  string
	now   time.Time
	admin Credential
	ctx   WorkContext
}

func newReviewFixture(t *testing.T) *reviewFixture {
	t.Helper()
	now := time.Date(2026, 9, 6, 1, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "review.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := New(Config{Store: db, Clock: func() time.Time { return now }, BootstrapCredential: "offline-secret"})
	if err != nil {
		t.Fatal(err)
	}
	_, admin, err := s.Bootstrap(context.Background(), BootstrapInput{Credential: "offline-secret", AdminName: "admin", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.CreateWorkspace(context.Background(), admin.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	c, err := s.CreateContext(context.Background(), admin.Token, CreateContextInput{WorkspaceID: w.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	return &reviewFixture{t: t, s: s, db: db, path: path, now: now, admin: admin, ctx: c}
}

func TestReviewDelegationCannotComposeGrantAndPayloadPermissions(t *testing.T) {
	f := newReviewFixture(t)
	_, intermediary, _, err := f.s.DelegateSession(context.Background(), f.admin.Token, DelegateInput{
		PrincipalName: "intermediary", TTL: 30 * time.Minute,
		Grants: []DelegatedGrant{
			{ContextID: f.ctx.ID, Permissions: PermissionGrant, ExpiresAt: f.now.Add(20 * time.Minute)},
			{ContextID: f.ctx.ID, Permissions: PermissionWrite, ExpiresAt: f.now.Add(20 * time.Minute)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = f.s.DelegateSession(context.Background(), intermediary.Token, DelegateInput{
		PrincipalName: "child", TTL: 10 * time.Minute,
		Grants: []DelegatedGrant{{ContextID: f.ctx.ID, Permissions: PermissionWrite, ExpiresAt: f.now.Add(10 * time.Minute)}},
	})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("split grants composed into delegation: %v", err)
	}
}

func TestReviewRevokedGrantAncestorInvalidatesDescendant(t *testing.T) {
	f := newReviewFixture(t)
	_, child, grants, err := f.s.DelegateSession(context.Background(), f.admin.Token, DelegateInput{
		PrincipalName: "child", TTL: 30 * time.Minute,
		Grants: []DelegatedGrant{{ContextID: f.ctx.ID, Permissions: PermissionRead | PermissionGrant, ExpiresAt: f.now.Add(20 * time.Minute)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, grandchild, _, err := f.s.DelegateSession(context.Background(), child.Token, DelegateInput{
		PrincipalName: "grandchild", TTL: 10 * time.Minute,
		Grants: []DelegatedGrant{{ContextID: f.ctx.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(10 * time.Minute)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.s.RevokeGrant(context.Background(), f.admin.Token, grants[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.GetContext(context.Background(), grandchild.Token, f.ctx.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("descendant survived ancestor grant revocation: %v", err)
	}
}

func TestReviewMessageIdempotencyAcrossStoreInstances(t *testing.T) {
	f := newReviewFixture(t)
	db2, err := OpenSQLite(f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	s2, err := New(Config{Store: db2, Clock: func() time.Time { return f.now }})
	if err != nil {
		t.Fatal(err)
	}
	const n = 12
	got := make(chan Message, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			svc := f.s
			if i%2 == 1 {
				svc = s2
			}
			m, e := svc.AppendMessage(context.Background(), f.admin.Token, f.ctx.ID, MessageInput{DedupeKey: "retry-1", Body: "payload"})
			got <- m
			errs <- e
		}()
	}
	wg.Wait()
	close(got)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	var id string
	for m := range got {
		if id == "" {
			id = m.ID
		}
		if m.ID != id || m.Sequence != 1 {
			t.Fatalf("non-idempotent result: %+v", m)
		}
	}
	msgs, err := f.s.ReplayMessages(context.Background(), f.admin.Token, f.ctx.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 {
		t.Fatalf("stored %d messages", len(msgs))
	}
}

func TestReviewRevokingIssuingSessionInvalidatesGrantToIndependentSession(t *testing.T) {
	f := newReviewFixture(t)
	p, err := f.s.CreatePrincipal(context.Background(), f.admin.Token, "independent")
	if err != nil {
		t.Fatal(err)
	}
	ind, err := f.s.MintSession(context.Background(), f.admin.Token, p.ID, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.GrantContext(context.Background(), f.admin.Token, GrantInput{SessionID: ind.Session.ID, ContextID: f.ctx.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(20 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.GetContext(context.Background(), ind.Token, f.ctx.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.s.RevokeSession(context.Background(), f.admin.Token, f.admin.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.s.GetContext(context.Background(), ind.Token, f.ctx.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("grant survived revocation of issuing session: %v", err)
	}
}

func TestReviewActiveLeaseCannotBeReclaimedWithoutFence(t *testing.T) {
	f := newReviewFixture(t)
	task, err := f.s.CreateTask(context.Background(), f.admin.Token, f.ctx.ID, TaskInput{Title: "exclusive"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := f.s.ClaimTask(context.Background(), f.admin.Token, task.ID, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.s.ClaimTask(context.Background(), f.admin.Token, task.ID, 10*time.Minute)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("active claim replaced without token: first=%d second=%d err=%v", first.Task.LeaseVersion, second.Task.LeaseVersion, err)
	}
}

func TestReviewForbiddenGetTaskReturnsNoPayload(t *testing.T) {
	f := newReviewFixture(t)
	task, err := f.s.CreateTask(context.Background(), f.admin.Token, f.ctx.ID, TaskInput{Title: "secret title", Body: "secret body"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := f.s.CreateWorkspace(context.Background(), f.admin.Token, "other")
	if err != nil {
		t.Fatal(err)
	}
	other, err := f.s.CreateContext(context.Background(), f.admin.Token, CreateContextInput{WorkspaceID: w.ID, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	_, outsider, _, err := f.s.DelegateSession(context.Background(), f.admin.Token, DelegateInput{PrincipalName: "outsider", TTL: 20 * time.Minute, Grants: []DelegatedGrant{{ContextID: other.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(10 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.s.GetTask(context.Background(), outsider.Token, task.ID)
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("want forbidden, got %v", err)
	}
	if got.ID != "" || got.Title != "" || got.Body != "" {
		t.Fatalf("forbidden payload leaked: %+v", got)
	}
}

func TestReviewRawCredentialIsNotPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.db")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Config{Store: db, BootstrapCredential: "bootstrap-plaintext"})
	if err != nil {
		t.Fatal(err)
	}
	_, cred, err := s.Bootstrap(context.Background(), BootstrapInput{Credential: "bootstrap-plaintext", AdminName: "admin", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte(cred.Token)) {
		t.Fatal("raw bearer token found in SQLite file")
	}
	if bytes.Contains(b, []byte("bootstrap-plaintext")) {
		t.Fatal("bootstrap credential found in SQLite file")
	}
}

func TestReviewBootstrapSingleUseAcrossStoreInstances(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.db")
	db1, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db1.Close()
	db2, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	s1, _ := New(Config{Store: db1, BootstrapCredential: "secret"})
	s2, _ := New(Config{Store: db2, BootstrapCredential: "secret"})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, svc := range []*Service{s1, s2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, e := svc.Bootstrap(context.Background(), BootstrapInput{Credential: "secret", AdminName: "admin", SessionTTL: time.Hour})
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	success, used := 0, 0
	for e := range errs {
		if e == nil {
			success++
		} else if errors.Is(e, ErrBootstrapUsed) {
			used++
		} else {
			t.Fatalf("unexpected bootstrap error: %v", e)
		}
	}
	if success != 1 || used != 1 {
		t.Fatalf("success=%d used=%d", success, used)
	}
}

func TestReviewMemoryStoresAreIsolated(t *testing.T) {
	db1, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db1.Close()
	db2, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	for i, db := range []*SQLiteStore{db1, db2} {
		svc, _ := New(Config{Store: db, BootstrapCredential: "secret"})
		if _, _, err = svc.Bootstrap(context.Background(), BootstrapInput{Credential: "secret", AdminName: "admin", SessionTTL: time.Hour}); err != nil {
			t.Fatalf("store %d shared state: %v", i, err)
		}
	}
}
