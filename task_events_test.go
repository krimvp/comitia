package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func taskEvents(t *testing.T, f fixture) []Message {
	t.Helper()
	m, err := f.svc.ReplayMessages(context.Background(), f.admin.Token, f.context.ID, 0, 100, MessageFilters{Schema: TaskEventSchema})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEveryTaskMutationRollsBackWithoutItsEvent(t *testing.T) {
	for _, action := range []string{"create", "renew", "complete", "release", "fail", "cancel"} {
		t.Run(action, func(t *testing.T) {
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
			before, err := f.svc.GetTask(ctx, f.admin.Token, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			good := f.svc.store
			f.svc.store = eventFailureStore{good, true}
			in := TaskOutcomeInput{ClaimToken: claim.Token, LeaseVersion: claim.Task.LeaseVersion, Reason: "stopped"}
			switch action {
			case "create":
				_, err = f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "rollback"})
			case "renew":
				_, err = f.svc.RenewClaim(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Hour)
			case "complete":
				_, err = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion)
			case "release":
				_, err = f.svc.ReleaseTask(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion)
			case "fail":
				_, err = f.svc.FailTask(ctx, f.admin.Token, task.ID, in)
			case "cancel":
				_, err = f.svc.CancelTask(ctx, f.admin.Token, task.ID, in)
			}
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("mutation error: %v", err)
			}
			f.svc.store = good
			after, err := f.svc.GetTask(ctx, f.admin.Token, task.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("partial mutation: %+v %v", after, err)
			}
			tasks, err := f.svc.ListTasks(ctx, f.admin.Token, f.context.ID, "", 10)
			if err != nil || len(tasks) != 1 || len(taskEvents(t, f)) != 2 {
				t.Fatal("partial event/task creation")
			}
			if _, err := f.svc.CompleteTask(ctx, f.admin.Token, task.ID, claim.Token, claim.Task.LeaseVersion); err != nil {
				t.Fatalf("rollback invalidated token: %v", err)
			}
		})
	}
}

func TestTaskEventWaitAndReconciliation(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(fmt.Sprint(separate), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "wait.db")
			f := setup(t, path)
			defer f.store.Close()
			ctx := context.Background()
			reader, _ := delegate(t, f, f.admin.Token, "reader", f.context.ID, PermissionRead, time.Hour)
			task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "job"})
			if err != nil {
				t.Fatal(err)
			}
			svc := f.svc
			if separate {
				store, err := OpenSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				svc, err = New(Config{Store: store, Clock: func() time.Time { return *f.now }})
				if err != nil {
					t.Fatal(err)
				}
			}
			read := make(chan struct{}, 1)
			svc.store = taskEventReadStore{svc.store, read}
			result := make(chan waitResult, 1)
			waitCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			go func() {
				messages, err := svc.WaitMessages(waitCtx, reader.Token, f.context.ID, 1, 2*time.Second, MessageFilters{Schema: TaskEventSchema, TaskID: task.ID, ForInstance: "reader"})
				result <- waitResult{messages, err}
			}()
			select {
			case <-read:
			case <-time.After(time.Second):
				t.Fatal("wait did not scan")
			}
			if _, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute); err != nil {
				t.Fatal(err)
			}
			got := receiveWait(t, result)
			if got.err != nil || len(got.messages) != 1 || got.messages[0].Sequence != 2 {
				t.Fatalf("task wait separate=%v: %+v", separate, got)
			}
		})
	}
}

type taskEventReadStore struct {
	Store
	read chan struct{}
}

func (s taskEventReadStore) View(ctx context.Context, fn func(ReadStore) error) error {
	err := s.Store.View(ctx, fn)
	select {
	case s.read <- struct{}{}:
	default:
	}
	return err
}

func TestTaskTransitionsAreAtomicServerMessages(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.db")
	f := setup(t, path)
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "job", OperationID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "job", OperationID: "create"}); err != nil {
		t.Fatal(err)
	}
	c, err := f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "claim", AgentInstance{ID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	c, err = f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "claim", AgentInstance{ID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	c, err = f.svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion, time.Minute, "renew")
	if err != nil {
		t.Fatal(err)
	}
	secret := c.Token
	if _, err = f.svc.ReleaseTask(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	c, err = f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	messages := taskEvents(t, f)
	for i, kind := range []string{"created", "claimed", "renewed", "released", "claimed", "completed"} {
		if len(messages) != 6 {
			t.Fatalf("events: %d", len(messages))
		}
		m := messages[i]
		var p TaskEventPayload
		if err := json.Unmarshal(m.Envelope.Payload, &p); err != nil {
			t.Fatal(err)
		}
		if m.Sequence != int64(i+1) || m.SessionID != f.admin.Session.ID || m.Envelope.TaskID != task.ID || p.Kind != kind || m.TaskAttempt != nil {
			t.Fatalf("event: %+v %+v", m, p)
		}
		if i == 1 && (p.LeaseOwnerInstance == nil || p.LeaseOwnerInstance.ID != "worker" || p.LeaseVersion != 1) {
			t.Fatalf("claim provenance: %+v", p)
		}
	}
	raw, _ := json.Marshal(messages)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), tokenHash(secret)) || strings.Contains(string(raw), c.Token) {
		t.Fatal("secret persisted in events")
	}
	if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "forge", Envelope: messages[0].Envelope}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged event: %v", err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc, err := New(Config{Store: store, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	f.store, f.svc = store, svc
	if got := taskEvents(t, f); len(got) != 6 || got[5].ID != messages[5].ID {
		t.Fatal("events lost on reopen")
	}
}

type eventFailureStore struct {
	Store
	failAfter bool
}
type eventFailureWriter struct {
	WriteStore
	failAfter bool
}

func (s eventFailureStore) Update(ctx context.Context, fn func(WriteStore) error) error {
	return s.Store.Update(ctx, func(w WriteStore) error { return fn(eventFailureWriter{w, s.failAfter}) })
}
func (w eventFailureWriter) PutMessage(m Message) (Message, error) {
	if w.failAfter {
		if _, err := w.WriteStore.PutMessage(m); err != nil {
			return Message{}, err
		}
	}
	return Message{}, ErrConflict
}

func TestTaskEventRollbackAndNotification(t *testing.T) {
	for _, after := range []bool{false, true} {
		f := setup(t, ":memory:")
		ctx := context.Background()
		task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "job"})
		if err != nil {
			t.Fatal(err)
		}
		watch, unsubscribe := f.svc.messageNotifications.subscribe(f.context.ID)
		changed := f.svc.messageNotifications.channel(watch)
		good := f.svc.store
		f.svc.store = eventFailureStore{good, after}
		if _, err = f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute); !errors.Is(err, ErrConflict) {
			t.Fatalf("append failure: %v", err)
		}
		select {
		case <-changed:
			t.Fatal("rollback woke waiters")
		default:
		}
		f.svc.store = good
		current, err := f.svc.GetTask(ctx, f.admin.Token, task.ID)
		if err != nil || current.Status != TaskPending || current.LeaseVersion != 0 {
			t.Fatalf("task survived rollback: %+v %v", current, err)
		}
		if got := taskEvents(t, f); len(got) != 1 {
			t.Fatal("event survived rollback")
		}
		if _, err = f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute); err != nil {
			t.Fatal(err)
		}
		select {
		case <-changed:
		default:
			t.Fatal("committed claim did not wake waiters")
		}
		if got := taskEvents(t, f); len(got) != 2 {
			t.Fatal("notified before durable append")
		}
		unsubscribe()
		f.store.Close()
	}
}
