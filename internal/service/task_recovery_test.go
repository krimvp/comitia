package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/sqlstore"
)

func TestTaskMutationRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.db")
	f := setup(t, path)
	ctx := context.Background()
	input := model.TaskInput{Title: "job", Body: "build", OperationID: "job-1"}
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, input)
	if err != nil || !reflect.DeepEqual(task, again) {
		t.Fatalf("create retry: %+v %v", again, err)
	}
	input.Body = "changed"
	if _, err = f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, input); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("changed create: %v", err)
	}
	owner := model.AgentInstance{ID: "process-1"}
	claim, err := f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "claim-1", owner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "job-1", owner); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("cross-method reuse: %v", err)
	}
	if _, err = f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, 2*time.Minute, "claim-1", owner); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("changed lease: %v", err)
	}
	if _, err = f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "claim-1", model.AgentInstance{ID: "process-2"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("changed instance: %v", err)
	}
	renewed, err := f.svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute, "renew-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion+1, time.Minute, "renew-1"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("changed renew version: %v", err)
	}
	if _, err = f.svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, "wrong", claim.Task.LeaseVersion, time.Minute, "renew-1"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("changed renew token: %v", err)
	}
	if err = f.store.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := sqlstore.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := New(model.Config{Store: st, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "claim-1", owner)
	if !errors.Is(err, model.ErrConflict) || got.Token != "" {
		t.Fatalf("old claim receipt after renewal: %v", err)
	}
	got, err = svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute, "renew-1")
	if err != nil || !reflect.DeepEqual(got.Task, renewed.Task) || got.Token == "" || got.Token == renewed.Token {
		t.Fatalf("renew retry after restart must rotate only token: %v", err)
	}
	if _, err = svc.CompleteTask(ctx, f.admin.Token, task.ID, renewed.Token, renewed.Task.LeaseVersion); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("lost renewed token remained valid: %v", err)
	}
	renewed = got
	peer := claimWorker(t, fixture{svc: svc, admin: f.admin, now: f.now}, f.context.ID)
	if _, err = svc.ClaimTaskWithOperation(ctx, peer.Token, task.ID, time.Minute, "claim-1", owner); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("peer recovered secret: %v", err)
	}
	list, err := svc.ListTasks(ctx, f.admin.Token, f.context.ID, "", 10)
	if err != nil || len(list) != 1 || list[0].LeaseVersion != renewed.Task.LeaseVersion {
		t.Fatalf("retry mutated tasks: %+v %v", list, err)
	}
	if _, err = svc.CompleteTask(ctx, f.admin.Token, task.ID, renewed.Token, renewed.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	got, err = svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute, "renew-1")
	if !errors.Is(err, model.ErrConflict) || got.Token != "" {
		t.Fatalf("historical receipt minted a token: %v", err)
	}
}

func TestTaskReceiptConcurrentRetries(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	const n = 12
	concurrent := func(call func() (model.TaskClaim, error)) []model.TaskClaim {
		t.Helper()
		results := make(chan model.TaskClaim, n)
		errs := make(chan error, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); c, err := call(); errs <- err; results <- c }()
		}
		wg.Wait()
		close(errs)
		close(results)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		out := []model.TaskClaim{}
		seen := map[string]bool{}
		for c := range results {
			if c.Token == "" || seen[c.Token] {
				t.Fatal("concurrent retry reused a token")
			}
			seen[c.Token] = true
			out = append(out, c)
		}
		for _, c := range out {
			if !reflect.DeepEqual(c.Task, out[0].Task) {
				t.Fatal("concurrent retry changed task outcome")
			}
		}
		return out
	}
	claims := concurrent(func() (model.TaskClaim, error) {
		task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "job", OperationID: "create"})
		if err != nil {
			return model.TaskClaim{}, err
		}
		return f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "claim")
	})
	taskID := claims[0].Task.ID
	// Retry serially before starting another logical operation: concurrent retries fence each other.
	current, err := f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, taskID, time.Minute, "claim")
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := f.svc.RenewClaimWithOperation(ctx, f.admin.Token, taskID, current.Token, current.Task.LeaseVersion, time.Minute, "renew")
	if err != nil {
		t.Fatal(err)
	}
	retries := concurrent(func() (model.TaskClaim, error) {
		return f.svc.RenewClaimWithOperation(ctx, f.admin.Token, taskID, current.Token, current.Task.LeaseVersion, time.Minute, "renew")
	})
	var stored model.Task
	if err = f.store.View(ctx, func(r model.ReadStore) error { var err error; stored, err = r.Task(taskID); return err }); err != nil {
		t.Fatal(err)
	}
	var latest model.TaskClaim
	for _, c := range append(claims, append([]model.TaskClaim{renewed}, retries...)...) {
		if tokenHash(c.Token) == stored.LeaseTokenHash {
			latest = c
			continue
		}
		if _, err = f.svc.CompleteTask(ctx, f.admin.Token, taskID, c.Token, stored.LeaseVersion); !errors.Is(err, model.ErrStaleClaim) {
			t.Fatalf("superseded response token valid: %v", err)
		}
	}
	if latest.Token == "" {
		t.Fatal("no returned token matches current lease")
	}
	tasks, err := f.svc.ListTasks(ctx, f.admin.Token, f.context.ID, "", 10)
	if err != nil || len(tasks) != 1 || tasks[0].LeaseVersion != 2 {
		t.Fatalf("duplicate mutation: %v", err)
	}
	if _, err = f.svc.CompleteTask(ctx, f.admin.Token, taskID, latest.Token, latest.Task.LeaseVersion); err != nil {
		t.Fatalf("last recovered token unusable: %v", err)
	}
}

func TestCurrentTokenWrongLeaseVersion(t *testing.T) {
	for _, method := range []string{"renew", "complete"} {
		for _, delta := range []int64{-1, 1} {
			t.Run(fmt.Sprintf("%s/%+d", method, delta), func(t *testing.T) {
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
				if method == "renew" {
					_, err = f.svc.RenewClaim(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion+delta, time.Minute)
				} else {
					_, err = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion+delta)
				}
				if !errors.Is(err, model.ErrStaleClaim) {
					t.Fatalf("current valid token with wrong version: %v", err)
				}
				if _, err = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion); err != nil {
					t.Fatalf("rejected request changed lease: %v", err)
				}
			})
		}
	}
}

func TestMessageTaskFence(t *testing.T) {
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
	input := model.MessageInput{DedupeKey: "result", Body: "done", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", TaskID: task.ID, Payload: json.RawMessage(`{}`)}, TaskFence: &model.TaskFence{TaskID: task.ID, ClaimToken: c.Token, LeaseVersion: c.Task.LeaseVersion}}
	for _, bad := range []model.TaskFence{{TaskID: task.ID, ClaimToken: c.Token, LeaseVersion: c.Task.LeaseVersion + 1}, {TaskID: task.ID, ClaimToken: "wrong", LeaseVersion: c.Task.LeaseVersion}} {
		copy := input
		copy.TaskFence = &bad
		if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, copy); !errors.Is(err, model.ErrStaleClaim) {
			t.Fatalf("bad fence: %v", err)
		}
	}
	accepted, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, input)
	if err != nil || accepted.TaskAttempt == nil || accepted.TaskAttempt.TaskID != task.ID || accepted.TaskAttempt.LeaseVersion != c.Task.LeaseVersion || accepted.SessionID != f.admin.Session.ID {
		t.Fatalf("attempt metadata: %+v %v", accepted, err)
	}
	raw, _ := json.Marshal(accepted)
	if strings.Contains(string(raw), c.Token) || strings.Contains(string(raw), tokenHash(c.Token)) {
		t.Fatalf("secret in result: %s", raw)
	}
	renewed, err := f.svc.RenewClaim(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, input)
	if err != nil || !reflect.DeepEqual(retry, accepted) {
		t.Fatalf("accepted result retry after rotation: %+v %v", retry, err)
	}
	changed := input
	changed.TaskFence = &model.TaskFence{TaskID: task.ID, ClaimToken: renewed.Token, LeaseVersion: renewed.Task.LeaseVersion}
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, changed); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("changed fence retry: %v", err)
	}
	changed.TaskFence = nil
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, changed); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("removed fence retry: %v", err)
	}
	input.DedupeKey = "stale-new-result"
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, input); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("rotated worker result: %v", err)
	}
	*f.now = f.now.Add(2 * time.Minute)
	input.TaskFence = &model.TaskFence{TaskID: task.ID, ClaimToken: renewed.Token, LeaseVersion: renewed.Task.LeaseVersion}
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, input); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("expired result: %v", err)
	}
	next, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, input); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("reassigned result: %v", err)
	}
	input.TaskFence = &model.TaskFence{TaskID: task.ID, ClaimToken: next.Token, LeaseVersion: next.Task.LeaseVersion}
	if _, err = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, next.Token, next.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, input); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("completed result: %v", err)
	}
	input.TaskFence = nil
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, input); err != nil {
		t.Fatalf("unfenced observer review: %v", err)
	}
	replay, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 10, model.MessageFilters{Schema: "comitia.discussion.v1"})
	if err != nil || len(replay) != 2 || !reflect.DeepEqual(replay[0], accepted) {
		t.Fatalf("persisted metadata/rejected appends: %+v %v", replay, err)
	}
}

func TestTaskRecoveryAuthorization(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	_, worker, _, err := f.svc.DelegateSession(ctx, f.admin.Token, model.DelegateInput{PrincipalName: "writer", TTL: time.Hour, Grants: []model.DelegatedGrant{{ContextID: f.context.ID, Permissions: model.PermissionRead | model.PermissionWrite | model.PermissionClaim, ExpiresAt: f.now.Add(10 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	in := model.TaskInput{Title: "job", OperationID: "create"}
	task, err := f.svc.CreateTask(ctx, worker.Token, f.context.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	// A different caller may use the same job key, but gets its own task.
	own, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, in)
	if err != nil || own.ID == task.ID {
		t.Fatalf("caller scope: %+v %v", own, err)
	}
	c, err := f.svc.ClaimTaskWithOperation(ctx, worker.Token, task.ID, model.OpenLease, "claim")
	if err != nil {
		t.Fatal(err)
	}
	inmsg := model.MessageInput{DedupeKey: "result", Body: "done", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", TaskID: task.ID, Payload: json.RawMessage(`{}`)}, TaskFence: &model.TaskFence{TaskID: task.ID, ClaimToken: c.Token, LeaseVersion: c.Task.LeaseVersion}}
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, inmsg); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("foreign owner with valid token: %v", err)
	}
	if _, err = f.svc.AppendMessage(ctx, worker.Token, f.context.ID, inmsg); err != nil {
		t.Fatal(err)
	}
	// The lease remains live, but the holder's claim grant has expired.
	*f.now = f.now.Add(11 * time.Minute)
	if _, err = f.svc.ClaimTaskWithOperation(ctx, worker.Token, task.ID, model.OpenLease, "claim"); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("receipt bypassed grant: %v", err)
	}
	if _, err = f.svc.CreateTask(ctx, worker.Token, f.context.ID, in); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("create receipt bypassed grant: %v", err)
	}
	// Keep write access, so claim permission is independently required for fences.
	if _, err = f.svc.GrantContext(ctx, f.admin.Token, model.GrantInput{SessionID: worker.Session.ID, ContextID: f.context.ID, Permissions: model.PermissionWrite, ExpiresAt: worker.Session.ExpiresAt}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.AppendMessage(ctx, worker.Token, f.context.ID, inmsg); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("fenced retry bypassed claim permission: %v", err)
	}
	inmsg.DedupeKey = "new-result"
	if _, err = f.svc.AppendMessage(ctx, worker.Token, f.context.ID, inmsg); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("fence bypassed claim permission: %v", err)
	}
	if err = f.svc.RevokeSession(ctx, f.admin.Token, worker.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.ClaimTaskWithOperation(ctx, worker.Token, task.ID, model.OpenLease, "claim"); !errors.Is(err, model.ErrUnauthenticated) {
		t.Fatalf("revoked receipt: %v", err)
	}
}

func TestTaskFenceReferencesAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fence.db")
	f := setup(t, path)
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "job"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	in := model.MessageInput{DedupeKey: "result", Body: "done", TaskFence: &model.TaskFence{TaskID: task.ID, ClaimToken: c.Token, LeaseVersion: c.Task.LeaseVersion}}
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("missing envelope: %v", err)
	}
	in.Envelope = &model.MessageEnvelope{Schema: "comitia.discussion.v1", TaskID: "another", Payload: json.RawMessage(`{}`)}
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("mismatched task: %v", err)
	}
	other, err := f.svc.CreateContext(ctx, f.admin.Token, model.CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	in.Envelope.TaskID = task.ID
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, other.ID, in); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("cross-context task: %v", err)
	}
	accepted, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := sqlstore.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := New(model.Config{Store: st, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in)
	if err != nil || !reflect.DeepEqual(retry, accepted) {
		t.Fatalf("persisted fence retry: %+v %v", retry, err)
	}
	in.TaskFence.ClaimToken = "wrong"
	if _, err = svc.AppendMessage(ctx, f.admin.Token, f.context.ID, in); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("changed token dedupe: %v", err)
	}
}

func TestTaskReceiptsRollbackWithMutation(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	trigger := func(enabled bool) {
		t.Helper()
		err := f.store.Update(ctx, func(w model.WriteStore) error {
			query := "DROP TRIGGER IF EXISTS fail_receipt"
			if enabled {
				query = "CREATE TRIGGER fail_receipt BEFORE INSERT ON task_mutation_receipts BEGIN SELECT RAISE(ABORT,'receipt failure'); END"
			}
			_, err := sqlstore.TxQuerier(w.(*sqlstore.TxStore)).Exec(query)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	trigger(true)
	got, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "job", OperationID: "create"})
	if err == nil || got.ID != "" {
		t.Fatalf("failed create returned result: %+v %v", got, err)
	}
	tasks, err := f.svc.ListTasks(ctx, f.admin.Token, f.context.ID, "", 10)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("create escaped rollback: %+v %v", tasks, err)
	}
	trigger(false)
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "job", OperationID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	trigger(true)
	c, err := f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "claim")
	if err == nil || c.Token != "" || c.Task.ID != "" {
		t.Fatalf("failed claim exposed token: %+v %v", c, err)
	}
	got, err = f.svc.GetTask(ctx, f.admin.Token, task.ID)
	if err != nil || got.Status != model.TaskPending || got.LeaseVersion != 0 {
		t.Fatalf("claim escaped rollback: %+v %v", got, err)
	}
	trigger(false)
	c, err = f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "claim")
	if err != nil {
		t.Fatal(err)
	}
	trigger(true)
	renewed, err := f.svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion, time.Minute, "renew")
	if err == nil || renewed.Token != "" {
		t.Fatalf("failed renew exposed token: %+v %v", renewed, err)
	}
	trigger(false)
	if _, err = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion); err != nil {
		t.Fatalf("renew escaped rollback: %v", err)
	}
}

func TestTaskOperationIDBound(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	bad := strings.Repeat("x", 257)
	if _, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "job", OperationID: bad}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("create oversized ID: %v", err)
	}
	if _, err := f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, "missing", time.Minute, bad); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("claim oversized ID: %v", err)
	}
	if _, err := f.svc.RenewClaimWithOperation(ctx, f.admin.Token, "missing", "token", 1, time.Minute, bad); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("renew oversized ID: %v", err)
	}
}

func TestLostClaimAndRenewResponses(t *testing.T) {
	for _, method := range []string{"claim", "renew"} {
		t.Run(method, func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			ctx := context.Background()
			task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "job"})
			if err != nil {
				t.Fatal(err)
			}
			call := func() (model.TaskClaim, error) {
				return f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "operation")
			}
			if method == "renew" {
				c, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				call = func() (model.TaskClaim, error) {
					return f.svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion, time.Minute, "operation")
				}
			}
			original, err := call()
			if err != nil {
				t.Fatal(err)
			}
			retry, err := call()
			if err != nil || !reflect.DeepEqual(original.Task, retry.Task) || retry.Token == "" || retry.Token == original.Token {
				t.Fatalf("lost %s response cannot recover with a fresh token: %v", method, err)
			}
		})
	}
}

func TestOldWorkerResultAfterReassignment(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "job"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	peer := claimWorker(t, f, f.context.ID)
	*f.now = f.now.Add(2 * time.Minute)
	if _, err = f.svc.ClaimTask(ctx, peer.Token, task.ID, time.Minute); err != nil {
		t.Fatal(err)
	}
	input := model.MessageInput{DedupeKey: "old-result", Body: "done", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", TaskID: task.ID, Payload: json.RawMessage(`{}`)}, TaskFence: &model.TaskFence{TaskID: task.ID, ClaimToken: old.Token, LeaseVersion: old.Task.LeaseVersion}}
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, input); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("old worker result after reassignment: %v", err)
	}
	messages, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 10, model.MessageFilters{Schema: "comitia.discussion.v1"})
	if err != nil || len(messages) != 0 {
		t.Fatalf("stale result entered history: %v %v", messages, err)
	}
	input.TaskFence = nil
	if _, err = f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, input); err != nil {
		t.Fatalf("unfenced observer review: %v", err)
	}
}

func TestTaskReceiptsStoreNoTaskTextAndRetriesRereadIt(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	in := model.TaskInput{Title: "receipt title", Body: strings.Repeat("large body ", 1000), OperationID: "create"}
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "claim")
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := f.svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute, "renew")
	if err != nil {
		t.Fatal(err)
	}
	retried, err := f.svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute, "renew")
	if err != nil || !reflect.DeepEqual(retried.Task, renewed.Task) {
		t.Fatalf("renew retry lost task text: %+v %v", retried.Task, err)
	}
	failure := model.TaskOutcomeInput{ClaimToken: retried.Token, LeaseVersion: retried.Task.LeaseVersion, Reason: "broken", OperationID: "fail"}
	failed, err := f.svc.FailTask(ctx, f.admin.Token, task.ID, failure)
	if err != nil {
		t.Fatal(err)
	}
	var stored int
	if err = sqlstore.DB(f.store).QueryRow("SELECT COUNT(*) FROM task_mutation_receipts WHERE result LIKE '%receipt title%' OR result LIKE '%large body%' OR LENGTH(result)>1024").Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("receipts persist task text: %d %v", stored, err)
	}
	again, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, in)
	if err != nil || !reflect.DeepEqual(again, task) {
		t.Fatalf("create retry lost task text: %+v %v", again, err)
	}
	if again, err = f.svc.FailTask(ctx, f.admin.Token, task.ID, failure); err != nil || !reflect.DeepEqual(again, failed) {
		t.Fatalf("fail retry lost task text: %+v %v", again, err)
	}
	// Receipts written by older binaries hold the full task and stay authoritative.
	legacy := task
	legacy.Title, legacy.Body = "legacy title", "legacy body"
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := taskFingerprint("create", f.context.ID, legacy.Title, legacy.Body)
	if _, err = sqlstore.DB(f.store).Exec("INSERT INTO task_mutation_receipts VALUES(?,?,?,?)", f.admin.Session.ID, "legacy", fingerprint, string(raw)); err != nil {
		t.Fatal(err)
	}
	again, err = f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: legacy.Title, Body: legacy.Body, OperationID: "legacy"})
	if err != nil || !reflect.DeepEqual(again, legacy) {
		t.Fatalf("legacy full receipt not honoured: %+v %v", again, err)
	}
}
