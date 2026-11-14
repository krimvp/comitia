package service

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/schema"
	"github.com/krimvp/comitia/internal/sqlstore"
)

var _ model.TaskOutcomeAPI = (*Service)(nil)

// The compile fixtures in task_recovery_compatibility_test.go also pin the
// original task API/read/write method sets for this capability.

func TestTaskOutcomesFencedAndDurable(t *testing.T) {
	for _, status := range []model.TaskStatus{model.TaskFailed, model.TaskCancelled} {
		t.Run(string(status), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "outcomes.db")
			f := setup(t, path)
			ctx := context.Background()
			worker, _ := delegate(t, f, f.admin.Token, "worker", f.context.ID, model.PermissionRead|model.PermissionClaim, time.Hour)
			task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "job"})
			if err != nil {
				t.Fatal(err)
			}
			c, err := f.svc.ClaimTask(ctx, worker.Token, task.ID, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			finish := f.svc.FailTask
			if status == model.TaskCancelled {
				finish = f.svc.CancelTask
			}
			in := model.TaskOutcomeInput{ClaimToken: c.Token, LeaseVersion: c.Task.LeaseVersion, Reason: "build failed", OperationID: "finish"}
			for _, bad := range []model.TaskOutcomeInput{{ClaimToken: "bad", LeaseVersion: in.LeaseVersion, Reason: in.Reason}, {ClaimToken: c.Token, LeaseVersion: in.LeaseVersion + 1, Reason: in.Reason}} {
				if _, err := finish(ctx, worker.Token, task.ID, bad); !errors.Is(err, model.ErrStaleClaim) {
					t.Fatalf("bad fence: %v", err)
				}
			}
			result, err := finish(ctx, worker.Token, task.ID, in)
			if err != nil || result.Status != status || result.OutcomeReason != in.Reason || result.LeaseTokenHash != "" || result.LeaseExpiresAt != nil {
				t.Fatalf("outcome: %+v %v", result, err)
			}
			if again, err := finish(ctx, worker.Token, task.ID, in); err != nil || again.Status != status {
				t.Fatalf("receipt retry: %+v %v", again, err)
			}
			changed := in
			changed.Reason = "changed"
			if _, err := finish(ctx, worker.Token, task.ID, changed); !errors.Is(err, model.ErrConflict) {
				t.Fatalf("changed receipt: %v", err)
			}
			if _, err := f.svc.ClaimTask(ctx, worker.Token, task.ID, time.Minute); !errors.Is(err, model.ErrConflict) {
				t.Fatalf("terminal reclaimed: %v", err)
			}
			if _, err := f.svc.RenewClaim(ctx, worker.Token, task.ID, c.Token, c.Task.LeaseVersion, time.Minute); !errors.Is(err, model.ErrStaleClaim) {
				t.Fatalf("terminal renewed: %v", err)
			}
			if _, err := f.svc.CompleteTask(ctx, worker.Token, task.ID, c.Token, c.Task.LeaseVersion); !errors.Is(err, model.ErrStaleClaim) {
				t.Fatalf("terminal completed: %v", err)
			}
			if _, err := f.svc.ReleaseTask(ctx, worker.Token, task.ID, c.Token, c.Task.LeaseVersion); err == nil {
				t.Fatal("terminal released")
			}
			payload, _ := json.Marshal(schema.TaskStatusPayload{State: "done", Summary: "advisory"})
			if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "late-result", Envelope: &model.MessageEnvelope{Schema: model.TaskStatusSchema, TaskID: task.ID, Payload: payload}, TaskFence: &model.TaskFence{TaskID: task.ID, ClaimToken: c.Token, LeaseVersion: c.Task.LeaseVersion}}); !errors.Is(err, model.ErrStaleClaim) {
				t.Fatalf("terminal fenced result: %v", err)
			}
			if len(taskEvents(t, f)) != 3 {
				t.Fatal("duplicate or rejected event")
			}
			assertNoTaskTokensAtRest(t, path, []string{c.Token})
			f.store.Close()
			assertNoTaskTokensAtRest(t, path, []string{c.Token})
			store, err := sqlstore.OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			svc, err := New(model.Config{Store: store, Clock: func() time.Time { return *f.now }})
			if err != nil {
				t.Fatal(err)
			}
			got, err := svc.GetTask(ctx, f.admin.Token, task.ID)
			if err != nil || got.Status != status || got.OutcomeReason != in.Reason {
				t.Fatalf("reopened: %+v %v", got, err)
			}
			for _, page := range []bool{false, true} {
				var tasks []model.Task
				if page {
					p, e := svc.ListTasksPage(ctx, f.admin.Token, f.context.ID, status, "", 10)
					tasks, err = p.Tasks, e
				} else {
					tasks, err = svc.ListTasks(ctx, f.admin.Token, f.context.ID, status, 10)
				}
				if err != nil || len(tasks) != 1 || tasks[0].OutcomeReason != in.Reason {
					t.Fatalf("filtered tasks page=%v: %+v %v", page, tasks, err)
				}
			}
		})
	}
}

func TestTaskCancellationAuthorityAndExpiredFences(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	worker, _ := delegate(t, f, f.admin.Token, "worker", f.context.ID, model.PermissionRead|model.PermissionClaim, time.Hour)
	peer, _ := delegate(t, f, f.admin.Token, "peer", f.context.ID, model.PermissionRead|model.PermissionWrite, time.Hour)
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "job"})
	if err != nil {
		t.Fatal(err)
	}
	in := model.TaskOutcomeInput{Reason: "cancel", LeaseVersion: 0}
	if _, err := f.svc.CancelTask(ctx, peer.Token, task.ID, in); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("writer cancellation: %v", err)
	}
	if _, err := f.svc.CancelTask(ctx, worker.Token, task.ID, in); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("claim permission without claim: %v", err)
	}
	c, err := f.svc.ClaimTask(ctx, worker.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CancelTask(ctx, f.admin.Token, task.ID, in); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("stale admin version: %v", err)
	}
	*f.now = f.now.Add(2 * time.Minute)
	in = model.TaskOutcomeInput{ClaimToken: c.Token, LeaseVersion: c.Task.LeaseVersion, Reason: "expired"}
	if _, err := f.svc.FailTask(ctx, worker.Token, task.ID, in); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("expired failure: %v", err)
	}
	if _, err := f.svc.CancelTask(ctx, worker.Token, task.ID, in); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("expired cancellation: %v", err)
	}
	in.ClaimToken = ""
	if _, err := f.svc.CancelTask(ctx, f.admin.Token, task.ID, in); err != nil {
		t.Fatalf("admin expired cancellation: %v", err)
	}
	pending, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CancelTask(ctx, f.admin.Token, pending.ID, model.TaskOutcomeInput{Reason: "not needed"}); err != nil {
		t.Fatalf("pending cancellation: %v", err)
	}
	for _, reason := range []string{"", " ", strings.Repeat("x", 4097), string([]byte{255})} {
		if _, err := f.svc.CancelTask(ctx, f.admin.Token, pending.ID, model.TaskOutcomeInput{Reason: reason}); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("invalid reason: %v", err)
		}
	}
	for _, finish := range []func(context.Context, string, string, model.TaskOutcomeInput) (model.Task, error){f.svc.FailTask, f.svc.CancelTask} {
		if _, err := finish(ctx, peer.Token, "missing", model.TaskOutcomeInput{Reason: "x"}); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("private missing: %v", err)
		}
	}
	if _, err := FailTask(ctx, oldTaskAPI{f.svc}, f.admin.Token, pending.ID, model.TaskOutcomeInput{Reason: "x"}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("optional failure API: %v", err)
	}
	if _, err := CancelTask(ctx, oldTaskAPI{f.svc}, f.admin.Token, pending.ID, model.TaskOutcomeInput{Reason: "x"}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("optional cancellation API: %v", err)
	}
}

func TestTaskStatusChildTaskLineage(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	a, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "A"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "B"})
	if err != nil {
		t.Fatal(err)
	}
	for _, childTask := range []string{a.ID, ""} {
		child, err := f.svc.CreateChildContext(ctx, f.admin.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, TaskID: childTask, Name: "child"})
		if err != nil {
			t.Fatal(err)
		}
		for _, state := range []string{"done", "failed", "cancelled"} {
			payload, _ := json.Marshal(schema.TaskStatusPayload{State: state, Summary: "report", ChildContextID: child.ID})
			_, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: child.ID + state, Envelope: &model.MessageEnvelope{Schema: model.TaskStatusSchema, TaskID: b.ID, Payload: payload}})
			if childTask != "" && !errors.Is(err, model.ErrInvalid) {
				t.Fatalf("cross-task child accepted: %v", err)
			}
			if childTask == "" && err != nil {
				t.Fatalf("taskless team child rejected: %v", err)
			}
		}
	}
	current, err := f.svc.GetTask(ctx, f.admin.Token, b.ID)
	if err != nil || current.Status != model.TaskPending {
		t.Fatalf("advisory reports mutated task: %+v %v", current, err)
	}
}

type outcomeReceiptFailureStore struct{ model.Store }
type outcomeReceiptFailureWriter struct {
	model.WriteStore
	model.TaskReceiptReadStore
}

func (s outcomeReceiptFailureStore) Update(ctx context.Context, fn func(model.WriteStore) error) error {
	return s.Store.Update(ctx, func(w model.WriteStore) error {
		return fn(outcomeReceiptFailureWriter{w, w.(model.TaskReceiptReadStore)})
	})
}
func (outcomeReceiptFailureWriter) PutTaskReceipt(string, string, model.TaskReceipt) error {
	return model.ErrConflict
}

func TestTaskOutcomeReceiptFailureRollsBackStateAndEvent(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "job"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	good := f.svc.store
	f.svc.store = outcomeReceiptFailureStore{f.store}
	if _, err := f.svc.FailTask(ctx, f.admin.Token, task.ID, model.TaskOutcomeInput{ClaimToken: c.Token, LeaseVersion: c.Task.LeaseVersion, Reason: "failed", OperationID: "failure"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("receipt failure: %v", err)
	}
	f.svc.store = good
	current, err := f.svc.GetTask(ctx, f.admin.Token, task.ID)
	if err != nil || current.Status != model.TaskLeased || current.OutcomeReason != "" || len(taskEvents(t, f)) != 2 {
		t.Fatalf("receipt rollback: %+v %v", current, err)
	}
	if _, err := f.svc.CompleteTask(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion); err != nil {
		t.Fatalf("receipt failure invalidated token: %v", err)
	}
}

func TestTaskOutcomeRevocationAndReassignment(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	worker, grant := delegate(t, f, f.admin.Token, "worker", f.context.ID, model.PermissionRead|model.PermissionClaim, time.Hour)
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "job"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.svc.ClaimTask(ctx, worker.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RevokeGrant(ctx, f.admin.Token, grant.ID); err != nil {
		t.Fatal(err)
	}
	in := model.TaskOutcomeInput{ClaimToken: c.Token, LeaseVersion: c.Task.LeaseVersion, Reason: "stopped"}
	for _, finish := range []func(context.Context, string, string, model.TaskOutcomeInput) (model.Task, error){f.svc.FailTask, f.svc.CancelTask} {
		if _, err := finish(ctx, worker.Token, task.ID, in); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("revoked grant: %v", err)
		}
	}
	peer, _ := delegate(t, f, f.admin.Token, "peer", f.context.ID, model.PermissionRead|model.PermissionClaim, time.Hour)
	if _, err := f.svc.ClaimTask(ctx, peer.Token, task.ID, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CancelTask(ctx, f.admin.Token, task.ID, in); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("old admin version after reassignment: %v", err)
	}
}
