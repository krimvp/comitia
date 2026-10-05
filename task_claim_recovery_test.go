package comitia

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

var _ TaskClaimRecoveryAPI = (*Service)(nil)

func TestRecoverClaimRotatesOnlyTokenAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.db")
	f := setup(t, path)
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "job"})
	if err != nil {
		t.Fatal(err)
	}
	lost, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute, AgentInstance{ID: "worker", Label: "Worker"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := New(Config{Store: st, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(10 * time.Second)
	recovered, err := svc.RecoverClaim(ctx, f.admin.Token, task.ID, lost.Task.LeaseVersion, AgentInstance{ID: "worker", Label: "different"})
	if err != nil || recovered.Token == "" || recovered.Token == lost.Token || !reflect.DeepEqual(recovered.Task, lost.Task) {
		t.Fatalf("recovery did not rotate only token: %v", err)
	}
	if _, err = svc.CompleteTask(ctx, f.admin.Token, task.ID, lost.Token, lost.Task.LeaseVersion); !errors.Is(err, ErrStaleClaim) {
		t.Fatalf("old token: %v", err)
	}
	messages, err := svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100)
	if err != nil || len(messages) != 2 {
		t.Fatalf("recovery emitted transition: %d %v", len(messages), err)
	}
	assertNoTaskTokensAtRest(t, path, []string{lost.Token, recovered.Token})
	if _, err = svc.CompleteTask(ctx, f.admin.Token, task.ID, recovered.Token, recovered.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	assertNoTaskTokensAtRest(t, path, []string{lost.Token, recovered.Token})
}

func TestRecoverClaimFencesHoldersAndVersions(t *testing.T) {
	for _, state := range []string{"other-session", "admin", "missing-instance", "other-instance", "stale-version", "expired", "renewed", "released", "completed", "failed", "cancelled", "reassigned", "revoked", "lost-claim", "lost-read", "private", "missing"} {
		t.Run(state, func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			ctx := context.Background()
			worker, grant := delegate(t, f, f.admin.Token, "worker", f.context.ID, PermissionRead|PermissionClaim, time.Hour)
			task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "job"})
			if err != nil {
				t.Fatal(err)
			}
			claim, err := f.svc.ClaimTask(ctx, worker.Token, task.ID, time.Minute, AgentInstance{ID: "worker"})
			if err != nil {
				t.Fatal(err)
			}
			raw, tid, version := worker.Token, task.ID, claim.Task.LeaseVersion
			instance := []AgentInstance{{ID: "worker"}}
			want := ErrStaleClaim
			switch state {
			case "other-session":
				raw = claimWorker(t, f, f.context.ID).Token
			case "admin":
				raw = f.admin.Token
			case "missing-instance":
				instance = nil
			case "other-instance":
				instance[0].ID = "other"
			case "stale-version":
				version++
			case "expired":
				*f.now = f.now.Add(time.Minute)
			case "renewed":
				_, err = f.svc.RenewClaim(ctx, raw, tid, claim.Token, version, time.Minute)
			case "released":
				_, err = f.svc.ReleaseTask(ctx, raw, tid, claim.Token, version)
			case "completed":
				_, err = f.svc.CompleteTask(ctx, raw, tid, claim.Token, version)
			case "failed":
				_, err = f.svc.FailTask(ctx, raw, tid, TaskOutcomeInput{ClaimToken: claim.Token, LeaseVersion: version, Reason: "failed"})
			case "cancelled":
				_, err = f.svc.CancelTask(ctx, raw, tid, TaskOutcomeInput{ClaimToken: claim.Token, LeaseVersion: version, Reason: "cancelled"})
			case "reassigned":
				*f.now = f.now.Add(time.Minute)
				_, err = f.svc.ClaimTask(ctx, f.admin.Token, tid, time.Minute)
			case "revoked":
				err = f.svc.RevokeSession(ctx, f.admin.Token, worker.Session.ID)
				want = ErrUnauthenticated
			case "lost-claim", "lost-read":
				err = f.svc.RevokeGrant(ctx, f.admin.Token, grant.ID)
				if err == nil {
					p := PermissionRead
					if state == "lost-read" {
						p = PermissionClaim
					}
					_, err = f.svc.GrantContext(ctx, f.admin.Token, GrantInput{SessionID: worker.Session.ID, ContextID: f.context.ID, Permissions: p, ExpiresAt: f.now.Add(time.Hour)})
				}
				want = ErrForbidden
			case "private":
				privateContext, e := f.svc.CreateContext(ctx, f.admin.Token, CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "private"})
				if e != nil {
					t.Fatal(e)
				}
				private := claimWorker(t, f, privateContext.ID)
				if e != nil {
					t.Fatal(e)
				}
				raw = private.Token
				want = ErrForbidden
			case "missing":
				tid = "missing"
				want = ErrForbidden
			}
			if err != nil {
				t.Fatal(err)
			}
			var before Task
			if err = f.store.View(ctx, func(r ReadStore) error { var e error; before, e = r.Task(task.ID); return e }); err != nil {
				t.Fatal(err)
			}
			got, err := f.svc.RecoverClaim(ctx, raw, tid, version, instance...)
			if !errors.Is(err, want) || got.Token != "" || got.Task.ID != "" {
				t.Fatalf("rejected recovery: %v", err)
			}
			if err = f.store.View(ctx, func(r ReadStore) error {
				after, e := r.Task(task.ID)
				if e == nil && !reflect.DeepEqual(before, after) {
					t.Error("rejected recovery changed lease")
				}
				return e
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRecoverClaimLegacyAndValidation(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "job"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RecoverClaim(ctx, oldTaskAPI{f.svc}, f.admin.Token, task.ID, claim.Task.LeaseVersion); !errors.Is(err, ErrInvalid) {
		t.Fatalf("legacy API: %v", err)
	}
	for _, tc := range []struct {
		tid      string
		version  int64
		instance []AgentInstance
	}{{"", 1, nil}, {task.ID, 0, nil}, {task.ID, -1, nil}, {task.ID, 1, []AgentInstance{{ID: "bad id"}}}, {task.ID, 1, []AgentInstance{{}}}, {task.ID, 1, []AgentInstance{{ID: "a"}, {ID: "b"}}}} {
		if _, err = f.svc.RecoverClaim(ctx, f.admin.Token, tc.tid, tc.version, tc.instance...); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid request: %v", err)
		}
	}
	// No new store capabilities are required either.
	svc, err := New(Config{Store: noReceiptStore{f.store}, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.RecoverClaim(ctx, f.admin.Token, task.ID, claim.Task.LeaseVersion, AgentInstance{ID: "configured"}); err != nil {
		t.Fatalf("legacy unscoped lease: %v", err)
	}
}

func TestRecoverClaimRollback(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "job"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.Update(ctx, func(w WriteStore) error {
		_, e := w.(*sqlStore).q.Exec("CREATE TRIGGER fail_recovery BEFORE UPDATE OF lease_token_hash ON tasks BEGIN SELECT RAISE(ABORT,'recovery failure'); END")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.RecoverClaim(ctx, f.admin.Token, task.ID, claim.Task.LeaseVersion)
	if err == nil || got.Token != "" || got.Task.ID != "" {
		t.Fatal("failed recovery exposed result")
	}
	if err = f.store.Update(ctx, func(w WriteStore) error { _, e := w.(*sqlStore).q.Exec("DROP TRIGGER fail_recovery"); return e }); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion); err != nil {
		t.Fatalf("hash escaped rollback: %v", err)
	}
}

func TestConcurrentRecoverClaimFencesAllButLastToken(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "job"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan TaskClaim, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := f.svc.RecoverClaim(ctx, f.admin.Token, task.ID, claim.Task.LeaseVersion)
			results <- c
			errs <- e
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	live := 0
	for c := range results {
		err = f.store.View(ctx, func(r ReadStore) error {
			current, e := r.Task(task.ID)
			if e == nil && current.LeaseTokenHash == tokenHash(c.Token) {
				live++
			}
			return e
		})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(c.Task, claim.Task) {
			t.Fatal("recovery changed lease")
		}
	}
	if live != 1 {
		t.Fatalf("live tokens=%d", live)
	}
}
