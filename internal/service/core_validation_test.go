package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/schema"

	"github.com/krimvp/comitia/internal/model"
)

func TestGrantsRejectUndefinedPermissionBits(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	worker := messageWorker(t, f, "worker", f.context.ID)
	for _, p := range []model.Permission{model.PermissionAll << 6, model.PermissionRead | 1<<20, ^model.Permission(0)} {
		if _, err := f.svc.GrantContext(ctx, f.admin.Token, model.GrantInput{SessionID: worker.Session.ID, ContextID: f.context.ID, Permissions: p, ExpiresAt: f.now.Add(time.Minute)}); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("grant %d: %v", p, err)
		}
		if _, _, _, err := f.svc.DelegateSession(ctx, f.admin.Token, model.DelegateInput{PrincipalName: "d", TTL: time.Hour, Grants: []model.DelegatedGrant{{ContextID: f.context.ID, Permissions: p, ExpiresAt: f.now.Add(time.Minute)}}}); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("delegate %d: %v", p, err)
		}
	}
}

// Claim alone works a task by ID; title and body stay behind read, as in GetTask.
func TestClaimOnlyHolderMutationsRedactTaskContent(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	worker, _ := delegate(t, f, f.admin.Token, "worker", f.context.ID, model.PermissionClaim, time.Hour)
	reader, _ := delegate(t, f, f.admin.Token, "reader", f.context.ID, model.PermissionRead|model.PermissionClaim, time.Hour)
	hidden := func(name string, task model.Task, err error) {
		t.Helper()
		if err != nil || task.ID == "" || task.Title != "" || task.Body != "" {
			t.Fatalf("%s leaked content: %+v %v", name, task, err)
		}
	}
	newTask := func() model.Task {
		task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "secret title", Body: "secret body"})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	task := newTask()
	if _, err := f.svc.GetTask(ctx, worker.Token, task.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("claim-only get: %v", err)
	}
	claim, err := f.svc.ClaimTaskWithOperation(ctx, worker.Token, task.ID, time.Minute, "claim-op")
	hidden("claim", claim.Task, err)
	claim, err = f.svc.ClaimTaskWithOperation(ctx, worker.Token, task.ID, time.Minute, "claim-op")
	hidden("claim retry", claim.Task, err)
	claim, err = f.svc.RenewClaim(ctx, worker.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute)
	hidden("renew", claim.Task, err)
	released, err := f.svc.ReleaseTask(ctx, worker.Token, task.ID, claim.Token, claim.Task.LeaseVersion)
	hidden("release", released, err)
	claim, err = f.svc.ClaimTask(ctx, worker.Token, task.ID, time.Minute)
	hidden("reclaim", claim.Task, err)
	done, err := f.svc.CompleteTask(ctx, worker.Token, task.ID, claim.Token, claim.Task.LeaseVersion)
	hidden("complete", done, err)
	for _, status := range []model.TaskStatus{model.TaskFailed, model.TaskCancelled} {
		task := newTask()
		claim, err := f.svc.ClaimTask(ctx, worker.Token, task.ID, time.Minute)
		hidden("claim", claim.Task, err)
		in := model.TaskOutcomeInput{ClaimToken: claim.Token, LeaseVersion: claim.Task.LeaseVersion, Reason: "stop", OperationID: string(status)}
		finish := f.svc.FailTask
		if status == model.TaskCancelled {
			finish = f.svc.CancelTask
		}
		result, err := finish(ctx, worker.Token, task.ID, in)
		hidden(string(status), result, err)
		result, err = finish(ctx, worker.Token, task.ID, in)
		hidden(string(status)+" retry", result, err)
		if stored, err := f.svc.GetTask(ctx, f.admin.Token, task.ID); err != nil || stored.Title != "secret title" || stored.Status != status {
			t.Fatalf("stored task changed: %+v %v", stored, err)
		}
	}
	task = newTask()
	claim, err = f.svc.ClaimTask(ctx, reader.Token, task.ID, time.Minute)
	if err != nil || claim.Task.Title != "secret title" || claim.Task.Body != "secret body" {
		t.Fatalf("reader claim: %+v %v", claim.Task, err)
	}
}

func TestValidationErrorsNameAcceptedValueAndField(t *testing.T) {
	err := schema.ValidApproval([]byte(`{"artifact_refs":["git:` + sha + `"],"verdict":"changes_requested","checks":["review"],"blocking_count":1}`))
	if !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "changes-requested") || strings.Contains(err.Error(), "changes_requested") {
		t.Fatalf("verdict error: %v", err)
	}
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	in := protocolInput("bad-responder", model.RequestSchema, "", schema.RequestPayload{Operation: "control.stop", Instructions: "Stop", ExpectedResponders: []string{f.admin.Session.ID, "missing"}, Deadline: "2099-01-01T00:00:00Z"})
	_, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in)
	if !errors.Is(err, model.ErrForbidden) || !strings.Contains(err.Error(), "expected_responders[1]") {
		t.Fatalf("responder error: %v", err)
	}
	if got := model.SafeReferenceMessage(err); !strings.Contains(got, "expected_responders[1]") {
		t.Fatalf("public responder message: %q", got)
	}
}

// Every adapter reaches these bounds, so none can mint an already-expired
// lease or session.
func TestLeaseAndSessionTTLBounds(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []time.Duration{time.Nanosecond, time.Second - 1, 24*time.Hour + 1, model.OpenLease - 1} {
		if _, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, d); !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "between 1s and 24h") {
			t.Fatalf("claim %v: %v", d, err)
		}
	}
	claim, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Second)
	if err != nil || !claim.Task.LeaseExpiresAt.Equal(f.now.Add(time.Second)) {
		t.Fatalf("minimum claim: %+v %v", claim.Task, err)
	}
	if _, err = f.svc.RenewClaim(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Millisecond); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("renew below minimum: %v", err)
	}
	if claim, err = f.svc.RenewClaim(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion, 24*time.Hour); err != nil {
		t.Fatalf("maximum renew: %v", err)
	}
	if _, err = f.svc.RenewClaim(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion, model.OpenLease); err != nil {
		t.Fatalf("open renew: %v", err)
	}
	p, err := f.svc.CreatePrincipal(ctx, f.admin.Token, "p")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.MintSession(ctx, f.admin.Token, p.ID, time.Second-1); !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "at least 1s") {
		t.Fatalf("mint: %v", err)
	}
	if _, err = f.svc.MintSession(ctx, f.admin.Token, p.ID, time.Second); err != nil {
		t.Fatalf("minimum mint: %v", err)
	}
	grants := []model.DelegatedGrant{{ContextID: f.context.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(time.Nanosecond)}}
	if _, _, _, err = f.svc.DelegateSession(ctx, f.admin.Token, model.DelegateInput{PrincipalName: "d", TTL: time.Nanosecond, Grants: grants}); !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "at least 1s") {
		t.Fatalf("delegate: %v", err)
	}
}
