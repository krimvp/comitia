package comitia

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type blockedWrites struct {
	Store
	block   bool
	updates int
}

func (s *blockedWrites) Update(ctx context.Context, fn func(WriteStore) error) error {
	s.updates++
	if s.block {
		<-ctx.Done()
		return ctx.Err()
	}
	return s.Store.Update(ctx, fn)
}

func TestStorageProbeBoundAndAuthorization(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	blocked := &blockedWrites{Store: f.store, block: true}
	svc, err := New(Config{Store: blocked, Clock: f.svc.now})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := f.svc.CreatePrincipal(ctx, f.admin.Token, "reader")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.svc.MintSession(ctx, f.admin.Token, p.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		token string
		want  error
	}{{"invalid", ErrUnauthenticated}, {c.Token, ErrForbidden}} {
		if _, err := svc.StorageStatus(ctx, tc.token); !errors.Is(err, tc.want) {
			t.Fatalf("auth: %v", err)
		}
	}
	if blocked.updates != 0 {
		t.Fatal("unauthorized probe attempted a write")
	}
	start := time.Now()
	status, err := svc.StorageStatus(ctx, f.admin.Token)
	if err != nil || status.WriteReady || status.WriteFailures != 1 || status.WriteAttempts != 1 || status.LastWriteErrorClass != "deadline_exceeded" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if elapsed := time.Since(start); elapsed < StorageProbeTimeout || elapsed > time.Second {
		t.Fatalf("probe took %s", elapsed)
	}
	if status.ProbeDurationMS < 200 || status.LastWriteDurationMS < 200 || status.MaxWriteDurationMS < status.LastWriteDurationMS {
		t.Fatalf("missing latency: %+v", status)
	}
	blocked.block = false
	next, err := svc.StorageStatus(ctx, f.admin.Token)
	if err != nil || !next.WriteReady || next.WriteFailures != 1 || next.WriteAttempts != 2 || next.LastWriteErrorClass != "deadline_exceeded" {
		t.Fatalf("recovery=%+v err=%v", next, err)
	}
	if _, err := svc.CreatePrincipal(ctx, f.admin.Token, "writer"); err != nil {
		t.Fatal(err)
	}
	if next, err = svc.StorageStatus(ctx, f.admin.Token); err != nil || !next.WriteReady || next.LastWriteErrorClass != "" {
		t.Fatalf("after real write=%+v err=%v", next, err)
	}
}

func TestStorageProbeDetectsReadOnlyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k.db")
	f := setup(t, path)
	f.store.Close()
	// mode=ro rejects writes with SQLITE_READONLY regardless of file
	// permissions (tests may run as root), while an empty BEGIN
	// IMMEDIATE/COMMIT still succeeds, as on an immutable file.
	db, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: path}).String()+"?mode=ro&_pragma=busy_timeout(10)&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	ro := &SQLiteStore{db: db, readers: db, writers: make(chan struct{}, 1)}
	defer ro.Close()
	svc, err := New(Config{Store: ro, Clock: f.svc.now})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := svc.CreatePrincipal(ctx, f.admin.Token, "writer"); FailureClass(err) != "storage_read_only" {
		t.Fatalf("write err=%v class=%s", err, FailureClass(err))
	}
	status, err := svc.StorageStatus(ctx, f.admin.Token)
	if err != nil || status.WriteReady || status.LastWriteErrorClass != "storage_read_only" || status.WriteFailures != 2 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestStorageProbeLeavesNoRows(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	count := func() (n int) {
		if err := f.store.db.QueryRow("SELECT count(*) FROM principals").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	if status, err := f.svc.StorageStatus(ctx, f.admin.Token); err != nil || !status.WriteReady {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if after := count(); after != before {
		t.Fatalf("probe left rows: %d -> %d", before, after)
	}
}

func TestStorageCountersConcurrentAndDomainRejection(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	store := f.svc.store.(*observedStore)
	store.mu.Lock()
	before := store.status
	store.mu.Unlock()
	const n = 12
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = store.Update(context.Background(), func(WriteStore) error { return ErrConflict })
		}()
	}
	wg.Wait()
	store.mu.Lock()
	after := store.status
	store.mu.Unlock()
	if after.WriteFailures != before.WriteFailures || after.WriteAttempts != before.WriteAttempts+n {
		t.Fatalf("before=%+v after=%+v", before, after)
	}
}
