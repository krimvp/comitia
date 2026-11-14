package service

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/sqlstore"
)

type blockedWrites struct {
	model.Store
	block   bool
	updates int
}

func (s *blockedWrites) Update(ctx context.Context, fn func(model.WriteStore) error) error {
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
	svc, err := New(model.Config{Store: blocked, Clock: f.svc.now})
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
	}{{"invalid", model.ErrUnauthenticated}, {c.Token, model.ErrForbidden}} {
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
	if elapsed := time.Since(start); elapsed < model.StorageProbeTimeout || elapsed > time.Second {
		t.Fatalf("probe took %s", elapsed)
	}
	if status.ProbeDurationMS < 200 || status.LastWriteDurationMS < 200 || status.MaxWriteDurationMS < status.LastWriteDurationMS {
		t.Fatalf("missing latency: %+v", status)
	}
	blocked.block = false
	// Each probe that is not ready counts one more failure.
	next, probes := awaitWriteReady(t, svc, f.admin.Token, func() {})
	if next.WriteFailures != uint64(probes) || next.WriteAttempts != uint64(probes)+1 || next.LastWriteErrorClass != "deadline_exceeded" {
		t.Fatalf("recovery=%+v probes=%d", next, probes)
	}
	// A real write before the probe clears the class; a probe that is not ready sets it again.
	next, _ = awaitWriteReady(t, svc, f.admin.Token, func() {
		if _, err := svc.CreatePrincipal(ctx, f.admin.Token, "writer"); err != nil {
			t.Fatal(err)
		}
	})
	if next.LastWriteErrorClass != "" {
		t.Fatalf("after real write=%+v", next)
	}
}

// awaitWriteReady runs prepare and the probe until the probe reports
// write_ready, and returns that status with the number of probes it took. The
// probe has a 250 ms budget, which a slow machine (-race, a loaded runner) can
// exceed; it then correctly reports write_ready false.
func awaitWriteReady(t *testing.T, svc *Service, token string, prepare func()) (model.StorageStatus, int) {
	t.Helper()
	for probes, deadline := 1, time.Now().Add(30*time.Second); ; probes++ {
		prepare()
		status, err := svc.StorageStatus(context.Background(), token)
		if err != nil {
			t.Fatalf("storage status: %v", err)
		}
		if status.WriteReady {
			return status, probes
		}
		if time.Now().After(deadline) {
			t.Fatalf("probe not write_ready: last %+v", status)
		}
		time.Sleep(10 * time.Millisecond)
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
	ro := sqlstore.NewStoreFromDB(db, db, make(chan struct{}, 1))
	defer ro.Close()
	svc, err := New(model.Config{Store: ro, Clock: f.svc.now})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := svc.CreatePrincipal(ctx, f.admin.Token, "writer"); model.FailureClass(err) != "storage_read_only" {
		t.Fatalf("write err=%v class=%s", err, model.FailureClass(err))
	}
	status, err := svc.StorageStatus(ctx, f.admin.Token)
	if err != nil || status.WriteReady || status.LastWriteErrorClass != "storage_read_only" || status.WriteFailures != 2 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
}

func TestStorageProbeLeavesNoRows(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	count := func() (n int) {
		if err := sqlstore.DB(f.store).QueryRow("SELECT count(*) FROM principals").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	awaitWriteReady(t, f.svc, f.admin.Token, func() {
		if after := count(); after != before {
			t.Fatalf("probe left rows: %d -> %d", before, after)
		}
	})
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
			_ = store.Update(context.Background(), func(model.WriteStore) error { return model.ErrConflict })
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

// slowCredentials makes the credential lookup outlast any deadline: under a
// context with a deadline the lookup waits for that context to end and fails
// with its error, as a store that honours cancellation but is slower than the
// deadline does; without a deadline it completes. slowView applies this to the
// lookup in View, slowWrite to the one inside Update; viewErr makes the lookup in
// View fail with that error instead.
type slowCredentials struct {
	model.Store
	slowView, slowWrite bool
	viewErr             error
	beforeUpdate        func()
	updates             int
}

// slowLookup is the lookup slowCredentials gives a ReadStore or WriteStore.
func slowLookup(ctx context.Context, err error, lookup func() (model.Session, error)) (model.Session, error) {
	if err != nil {
		return model.Session{}, err
	}
	if _, ok := ctx.Deadline(); ok {
		<-ctx.Done()
		return model.Session{}, ctx.Err()
	}
	return lookup()
}

type slowRead struct {
	model.ReadStore
	ctx context.Context
	err error
}

func (r slowRead) SessionByTokenHash(h string) (model.Session, error) {
	return slowLookup(r.ctx, r.err, func() (model.Session, error) { return r.ReadStore.SessionByTokenHash(h) })
}

type slowWrite struct {
	model.WriteStore
	ctx context.Context
}

func (w slowWrite) SessionByTokenHash(h string) (model.Session, error) {
	return slowLookup(w.ctx, nil, func() (model.Session, error) { return w.WriteStore.SessionByTokenHash(h) })
}

func (s *slowCredentials) View(ctx context.Context, fn func(model.ReadStore) error) error {
	return s.Store.View(ctx, func(r model.ReadStore) error {
		if s.slowView || s.viewErr != nil {
			return fn(slowRead{ReadStore: r, ctx: ctx, err: s.viewErr})
		}
		return fn(r)
	})
}

func (s *slowCredentials) Update(ctx context.Context, fn func(model.WriteStore) error) error {
	s.updates++
	if s.beforeUpdate != nil {
		s.beforeUpdate()
	}
	return s.Store.Update(ctx, func(w model.WriteStore) error {
		if s.slowWrite {
			return fn(slowWrite{WriteStore: w, ctx: ctx})
		}
		return fn(w)
	})
}

type storageIOError struct{}

func (storageIOError) Error() string { return "disk I/O error" }
func (storageIOError) Code() int     { return 10 }

// A valid administrator must not be told unauthenticated because the credential
// lookup was slower than the probe's budget, while refused credentials stay
// refused and learn nothing about storage.
func TestStorageStatusSlowCredentialLookup(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	slow := &slowCredentials{Store: f.store}
	svc, err := New(model.Config{Store: slow, Clock: f.svc.now})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.CreatePrincipal(ctx, f.admin.Token, "reader")
	if err != nil {
		t.Fatal(err)
	}
	reader, err := f.svc.MintSession(ctx, f.admin.Token, p.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	revoked, err := f.svc.MintSession(ctx, f.admin.Token, f.admin.Session.PrincipalID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RevokeSession(ctx, f.admin.Token, revoked.Session.ID); err != nil {
		t.Fatal(err)
	}

	slow.slowView = true
	awaitWriteReady(t, svc, f.admin.Token, func() {})
	probes := slow.updates
	for _, tc := range []struct {
		name, token string
		want        error
	}{{"invalid", "invalid", model.ErrUnauthenticated}, {"revoked", revoked.Token, model.ErrUnauthenticated}, {"non-admin", reader.Token, model.ErrForbidden}} {
		if status, err := svc.StorageStatus(ctx, tc.token); !errors.Is(err, tc.want) || status != (model.StorageStatus{}) {
			t.Fatalf("%s: status=%+v err=%v", tc.name, status, err)
		}
	}
	if slow.updates != probes {
		t.Fatalf("a refused caller reached the write probe: updates=%d, want %d", slow.updates, probes)
	}

	slow.slowView, slow.slowWrite = false, true
	status, err := svc.StorageStatus(ctx, f.admin.Token)
	if err != nil || status.WriteReady || status.LastWriteErrorClass != "deadline_exceeded" {
		t.Fatalf("slow write lookup: status=%+v err=%v", status, err)
	}
	slow.slowWrite = false

	// The write re-checks the credential: one revoked after the View is refused.
	second, err := f.svc.MintSession(ctx, f.admin.Token, f.admin.Session.PrincipalID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	slow.beforeUpdate = func() {
		if err := f.svc.RevokeSession(ctx, f.admin.Token, second.Session.ID); err != nil {
			t.Error(err)
		}
	}
	if status, err := svc.StorageStatus(ctx, second.Token); !errors.Is(err, model.ErrUnauthenticated) || status != (model.StorageStatus{}) {
		t.Fatalf("revoked during probe: status=%+v err=%v", status, err)
	}
	slow.beforeUpdate = nil

	// A storage failure in the lookup is not an authentication failure, and a
	// caller not known to be an administrator learns no storage class from it.
	slow.viewErr = storageIOError{}
	before := slow.updates
	status, err = svc.StorageStatus(ctx, f.admin.Token)
	if err == nil || errors.Is(err, model.ErrUnauthenticated) || model.FailureClass(err) != "internal" || status != (model.StorageStatus{}) || slow.updates != before {
		t.Fatalf("lookup failure: status=%+v err=%v class=%s", status, err, model.FailureClass(err))
	}
	slow.viewErr = nil

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := svc.StorageStatus(cancelled, f.admin.Token); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller: %v", err)
	}
}
