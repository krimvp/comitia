package comitia

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/sqlstore"
)

// These method sets are the task contracts from before recovery was added.
type originalTaskAPI interface {
	CreateTask(context.Context, string, string, TaskInput) (Task, error)
	GetTask(context.Context, string, string) (Task, error)
	ListTasks(context.Context, string, string, TaskStatus, int) ([]Task, error)
	ClaimTask(context.Context, string, string, time.Duration, ...AgentInstance) (TaskClaim, error)
	RenewClaim(context.Context, string, string, string, int64, time.Duration) (TaskClaim, error)
	CompleteTask(context.Context, string, string, string, int64) (Task, error)
	ReleaseTask(context.Context, string, string, string, int64) (Task, error)
}
type originalTaskReader interface {
	Task(string) (Task, error)
	Tasks(string, TaskStatus, int) ([]Task, error)
}
type originalTaskWriter interface{ PutTask(Task) error }
type oldTaskAPI struct{ originalTaskAPI }
type oldTaskReader struct{ originalTaskReader }
type oldTaskWriter struct{ originalTaskWriter }

var _ TaskAPI = oldTaskAPI{}
var _ TaskReadStore = oldTaskReader{}
var _ TaskWriteStore = oldTaskWriter{}
var _ TaskMutationAPI = (*Service)(nil)
var _ TaskReceiptReadStore = (*sqlstore.TxStore)(nil)
var _ TaskReceiptWriteStore = (*sqlstore.TxStore)(nil)

type noReceiptStore struct{ Store }

func (s noReceiptStore) View(ctx context.Context, f func(ReadStore) error) error {
	return s.Store.View(ctx, func(r ReadStore) error { return f(struct{ ReadStore }{r}) })
}
func (s noReceiptStore) Update(ctx context.Context, f func(WriteStore) error) error {
	return s.Store.Update(ctx, func(w WriteStore) error { return f(struct{ WriteStore }{w}) })
}

func TestTaskRecoveryOptionalCapabilities(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	legacy := oldTaskAPI{f.svc}
	if _, err := CreateTaskOperation(ctx, legacy, f.admin.Token, f.context.ID, TaskInput{Title: "job", OperationID: "create"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported creation API: %v", err)
	}
	task, err := CreateTaskOperation(ctx, legacy, f.admin.Token, f.context.ID, TaskInput{Title: "job"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ClaimTaskOperation(ctx, legacy, f.admin.Token, task.ID, time.Minute, "operation"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported recovery API: %v", err)
	}
	if _, err = RenewClaimOperation(ctx, legacy, f.admin.Token, task.ID, "token", 1, time.Minute, "operation"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported renewal API: %v", err)
	}
	c, err := ClaimTaskOperation(ctx, legacy, f.admin.Token, task.ID, time.Minute, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = RenewClaimOperation(ctx, legacy, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion, time.Minute, ""); err != nil {
		t.Fatalf("ordinary API renewal: %v", err)
	}
	svc, err := New(Config{Store: noReceiptStore{f.store}, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "unsupported", OperationID: "create"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported recovery store: %v", err)
	}
	task, err = svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "claim"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported claim store: %v", err)
	}
	c, err = svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion, time.Minute, "renew"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported renew store: %v", err)
	}
	c, err = svc.RenewClaim(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CompleteTask(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion); err != nil {
		t.Fatalf("ordinary legacy store completion: %v", err)
	}
}
