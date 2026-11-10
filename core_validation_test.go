package comitia

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGrantsRejectUndefinedPermissionBits(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	worker := messageWorker(t, f, "worker", f.context.ID)
	for _, p := range []Permission{PermissionAll << 6, PermissionRead | 1<<20, ^Permission(0)} {
		if _, err := f.svc.GrantContext(ctx, f.admin.Token, GrantInput{SessionID: worker.Session.ID, ContextID: f.context.ID, Permissions: p, ExpiresAt: f.now.Add(time.Minute)}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("grant %d: %v", p, err)
		}
		if _, _, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{PrincipalName: "d", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: p, ExpiresAt: f.now.Add(time.Minute)}}}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("delegate %d: %v", p, err)
		}
	}
}

// Claim alone works a task by ID; title and body stay behind read, as in GetTask.
func TestClaimOnlyHolderMutationsRedactTaskContent(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	worker, _ := delegate(t, f, f.admin.Token, "worker", f.context.ID, PermissionClaim, time.Hour)
	reader, _ := delegate(t, f, f.admin.Token, "reader", f.context.ID, PermissionRead|PermissionClaim, time.Hour)
	hidden := func(name string, task Task, err error) {
		t.Helper()
		if err != nil || task.ID == "" || task.Title != "" || task.Body != "" {
			t.Fatalf("%s leaked content: %+v %v", name, task, err)
		}
	}
	newTask := func() Task {
		task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "secret title", Body: "secret body"})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	task := newTask()
	if _, err := f.svc.GetTask(ctx, worker.Token, task.ID); !errors.Is(err, ErrForbidden) {
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
	for _, status := range []TaskStatus{TaskFailed, TaskCancelled} {
		task := newTask()
		claim, err := f.svc.ClaimTask(ctx, worker.Token, task.ID, time.Minute)
		hidden("claim", claim.Task, err)
		in := TaskOutcomeInput{ClaimToken: claim.Token, LeaseVersion: claim.Task.LeaseVersion, Reason: "stop", OperationID: string(status)}
		finish := f.svc.FailTask
		if status == TaskCancelled {
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
	err := validApproval([]byte(`{"artifact_refs":["git:` + sha + `"],"verdict":"changes_requested","checks":["review"],"blocking_count":1}`))
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "changes-requested") || strings.Contains(err.Error(), "changes_requested") {
		t.Fatalf("verdict error: %v", err)
	}
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	in := protocolInput("bad-responder", RequestSchema, "", RequestPayload{Operation: "control.stop", Instructions: "Stop", ExpectedResponders: []string{f.admin.Session.ID, "missing"}, Deadline: "2099-01-01T00:00:00Z"})
	_, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in)
	if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "expected_responders[1]") {
		t.Fatalf("responder error: %v", err)
	}
	if got := safeReferenceMessage(err); !strings.Contains(got, "expected_responders[1]") {
		t.Fatalf("public responder message: %q", got)
	}
}
