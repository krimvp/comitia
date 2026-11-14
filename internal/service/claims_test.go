package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/sqlstore"
)

func claimWorker(t *testing.T, f fixture, cid string) model.Credential {
	t.Helper()
	_, c, _, err := f.svc.DelegateSession(context.Background(), f.admin.Token, model.DelegateInput{PrincipalName: "worker", TTL: time.Hour, Grants: []model.DelegatedGrant{{ContextID: cid, Permissions: model.PermissionRead | model.PermissionClaim, ExpiresAt: f.now.Add(time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClaimRecordsInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claims.db")
	f := setup(t, path)
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "build"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]model.AgentInstance{{{ID: "bad id"}}, {{ID: "a"}, {ID: "b"}}, {{ID: "a", Label: "x\n"}}} {
		if _, err = f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute, bad...); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("instance %v: %v", bad, err)
		}
	}
	want := model.AgentInstance{ID: "claude-repo-18", Label: "issue 18"}
	claim, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute, want)
	if err != nil || claim.Task.LeaseOwnerInstance == nil || *claim.Task.LeaseOwnerInstance != want {
		t.Fatalf("claim: %+v %v", claim.Task, err)
	}
	renewed, err := f.svc.RenewClaim(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute)
	if err != nil || renewed.Task.LeaseOwnerInstance == nil || *renewed.Task.LeaseOwnerInstance != want {
		t.Fatalf("renew kept instance: %+v %v", renewed.Task, err)
	}
	f.store.Close()
	st, err := sqlstore.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := New(model.Config{Store: st, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetTask(ctx, f.admin.Token, task.ID)
	if err != nil || got.LeaseOwnerInstance == nil || *got.LeaseOwnerInstance != want {
		t.Fatalf("get: %+v %v", got, err)
	}
	list, err := svc.ListTasks(ctx, f.admin.Token, f.context.ID, model.TaskLeased, 10)
	if err != nil || len(list) != 1 || list[0].LeaseOwnerInstance == nil || *list[0].LeaseOwnerInstance != want {
		t.Fatalf("list: %+v %v", list, err)
	}
	done, err := svc.CompleteTask(ctx, f.admin.Token, task.ID, renewed.Token, renewed.Task.LeaseVersion)
	if err != nil || done.LeaseOwnerInstance == nil || *done.LeaseOwnerInstance != want {
		t.Fatalf("completed task keeps its last holder: %+v %v", done, err)
	}

	// A claim without an instance, after an expired one, must not inherit it.
	other, err := svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ClaimTask(ctx, f.admin.Token, other.ID, time.Minute, want); err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(2 * time.Minute)
	fresh, err := svc.ClaimTask(ctx, f.admin.Token, other.ID, time.Minute)
	if err != nil || fresh.Task.LeaseOwnerInstance != nil {
		t.Fatalf("reclaim: %+v %v", fresh.Task, err)
	}
	if got, err = svc.GetTask(ctx, f.admin.Token, other.ID); err != nil || got.LeaseOwnerInstance != nil {
		t.Fatalf("stale instance persisted: %+v %v", got, err)
	}
}

func TestOpenLeaseIsBoundedBySession(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	worker := claimWorker(t, f, f.context.ID)
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "long"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.svc.ClaimTask(ctx, worker.Token, task.ID, model.OpenLease)
	if err != nil || claim.Task.LeaseExpiresAt == nil || !claim.Task.LeaseExpiresAt.Equal(worker.Session.ExpiresAt) {
		t.Fatalf("open lease: %+v %v", claim.Task, err)
	}
	renewed, err := f.svc.RenewClaim(ctx, worker.Token, task.ID, claim.Token, claim.Task.LeaseVersion, model.OpenLease)
	if err != nil || !renewed.Task.LeaseExpiresAt.Equal(worker.Session.ExpiresAt) {
		t.Fatalf("open renew: %+v %v", renewed.Task, err)
	}
	short, err := f.svc.RenewClaim(ctx, worker.Token, task.ID, renewed.Token, renewed.Task.LeaseVersion, time.Minute)
	if err != nil || !short.Task.LeaseExpiresAt.Equal(f.now.Add(time.Minute)) {
		t.Fatalf("short renew: %+v %v", short.Task, err)
	}
}

func TestReleaseTask(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	worker := claimWorker(t, f, f.context.ID)
	peer := claimWorker(t, f, f.context.ID)
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "release"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ReleaseTask(ctx, f.admin.Token, task.ID, "", 0); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("release pending: %v", err)
	}
	claim, err := f.svc.ClaimTask(ctx, worker.Token, task.ID, model.OpenLease, model.AgentInstance{ID: "worker-1"})
	if err != nil {
		t.Fatal(err)
	}
	v := claim.Task.LeaseVersion
	if _, err = f.svc.ReleaseTask(ctx, peer.Token, task.ID, "", v); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("non-admin release without token: %v", err)
	}
	if _, err = f.svc.ReleaseTask(ctx, peer.Token, task.ID, claim.Token, v); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("another session used the holder's token: %v", err)
	}
	if _, err = f.svc.ReleaseTask(ctx, worker.Token, task.ID, "ct_wrong", v); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("wrong token: %v", err)
	}
	if _, err = f.svc.ReleaseTask(ctx, worker.Token, task.ID, claim.Token, v+1); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("wrong version: %v", err)
	}
	released, err := f.svc.ReleaseTask(ctx, worker.Token, task.ID, claim.Token, v)
	if err != nil || released.Status != model.TaskPending || released.LeaseOwnerSessionID != "" || released.LeaseOwnerInstance != nil || released.LeaseExpiresAt != nil || released.LeaseVersion != v+1 {
		t.Fatalf("holder release: %+v %v", released, err)
	}
	if _, err = f.svc.CompleteTask(ctx, worker.Token, task.ID, claim.Token, v); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("released token still works: %v", err)
	}

	// A supervising admin frees a silent worker's open lease without its token.
	claim, err = f.svc.ClaimTask(ctx, worker.Token, task.ID, model.OpenLease, model.AgentInstance{ID: "worker-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ReleaseTask(ctx, f.admin.Token, task.ID, "", claim.Task.LeaseVersion-1); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("admin stale version: %v", err)
	}
	released, err = f.svc.ReleaseTask(ctx, f.admin.Token, task.ID, "", claim.Task.LeaseVersion)
	if err != nil || released.Status != model.TaskPending {
		t.Fatalf("admin release: %+v %v", released, err)
	}
	if _, err = f.svc.RenewClaim(ctx, worker.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("renew after admin release: %v", err)
	}
	next, err := f.svc.ClaimTask(ctx, peer.Token, task.ID, time.Minute)
	if err != nil || next.Task.LeaseOwnerSessionID != peer.Session.ID {
		t.Fatalf("reclaim after release: %+v %v", next.Task, err)
	}
	if _, err = f.svc.CompleteTask(ctx, peer.Token, task.ID, next.Token, next.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ReleaseTask(ctx, f.admin.Token, task.ID, "", next.Task.LeaseVersion); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("release completed: %v", err)
	}
}

func TestDeadHolderLeaseCanBeReclaimed(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	worker := claimWorker(t, f, f.context.ID)
	peer := claimWorker(t, f, f.context.ID)
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "orphan"})
	if err != nil {
		t.Fatal(err)
	}
	held, err := f.svc.ClaimTask(ctx, worker.Token, task.ID, model.OpenLease, model.AgentInstance{ID: "worker-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ClaimTask(ctx, peer.Token, task.ID, time.Minute); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("live holder: %v", err)
	}
	if err = f.svc.RevokeSession(ctx, f.admin.Token, worker.Session.ID); err != nil {
		t.Fatal(err)
	}
	took, err := f.svc.ClaimTask(ctx, peer.Token, task.ID, time.Minute)
	if err != nil || took.Task.LeaseOwnerSessionID != peer.Session.ID || took.Task.LeaseVersion != held.Task.LeaseVersion+1 || took.Task.LeaseOwnerInstance != nil {
		t.Fatalf("revoked holder: %+v %v", took.Task, err)
	}

	// A holder that lost its claim grant is equally unable to finish.
	other, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "ungranted"})
	if err != nil {
		t.Fatal(err)
	}
	_, reader, _, err := f.svc.DelegateSession(ctx, f.admin.Token, model.DelegateInput{PrincipalName: "short", TTL: time.Hour, Grants: []model.DelegatedGrant{{ContextID: f.context.ID, Permissions: model.PermissionRead | model.PermissionClaim, ExpiresAt: f.now.Add(10 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ClaimTask(ctx, reader.Token, other.ID, model.OpenLease); err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(20 * time.Minute)
	if took, err = f.svc.ClaimTask(ctx, peer.Token, other.ID, time.Minute); err != nil || took.Task.LeaseOwnerSessionID != peer.Session.ID {
		t.Fatalf("holder without grant: %+v %v", took.Task, err)
	}
}
