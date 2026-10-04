package comitia

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func assertNoTaskTokensAtRest(t *testing.T, path string, tokens []string) {
	t.Helper()
	for _, file := range []string{path, path + "-wal"} {
		raw, err := os.ReadFile(file)
		if errors.Is(err, os.ErrNotExist) && file != path {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range tokens {
			if token != "" && bytes.Contains(raw, []byte(token)) {
				t.Fatalf("plaintext claim token persisted in %s", filepath.Base(file))
			}
		}
	}
}

func TestTaskReceiptTokensAbsentAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tokens.db")
	f := setup(t, path)
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "job", OperationID: "create"})
	if err != nil {
		t.Fatal(err)
	}
	tokens := []string{}
	check := func(c TaskClaim) {
		t.Helper()
		tokens = append(tokens, c.Token)
		assertNoTaskTokensAtRest(t, path, tokens)
	}
	c, err := f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "claim")
	if err != nil {
		t.Fatal(err)
	}
	check(c)
	recovered, err := f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "claim")
	if err != nil {
		t.Fatal(err)
	}
	check(recovered)
	renewed, err := f.svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, recovered.Token, recovered.Task.LeaseVersion, time.Minute, "renew")
	if err != nil {
		t.Fatal(err)
	}
	check(renewed)
	recovered, err = f.svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, recovered.Token, recovered.Task.LeaseVersion, time.Minute, "renew")
	if err != nil {
		t.Fatal(err)
	}
	check(recovered)
	if err = f.store.Close(); err != nil {
		t.Fatal(err)
	}
	assertNoTaskTokensAtRest(t, path, tokens)
}

func TestTaskReceiptRecoveryRotatesLostToken(t *testing.T) {
	for _, method := range []string{"claim", "renew"} {
		t.Run(method, func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			ctx := context.Background()
			task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "job"})
			if err != nil {
				t.Fatal(err)
			}
			call := func() (TaskClaim, error) {
				return f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "operation")
			}
			if method == "renew" {
				c, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				call = func() (TaskClaim, error) {
					return f.svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion, time.Minute, "operation")
				}
			}
			lost, err := call()
			if err != nil {
				t.Fatal(err)
			}
			*f.now = f.now.Add(10 * time.Second)
			recovered, err := call()
			if err != nil || recovered.Token == "" || recovered.Token == lost.Token || !reflect.DeepEqual(recovered.Task, lost.Task) {
				t.Fatalf("retry must rotate only token: %v", err)
			}
			if _, err = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, lost.Token, lost.Task.LeaseVersion); !errors.Is(err, ErrStaleClaim) {
				t.Fatalf("lost token was not fenced: %v", err)
			}
			if _, err = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, recovered.Token, recovered.Task.LeaseVersion); err != nil {
				t.Fatalf("recovered token not usable: %v", err)
			}
		})
	}
}

func TestTaskReceiptRetriesAfterLeaseMovedOn(t *testing.T) {
	for _, method := range []string{"claim", "renew"} {
		for _, transition := range []string{"completed", "released", "expired", "reclaimed", "renewed"} {
			t.Run(method+"/"+transition, func(t *testing.T) {
				f := setup(t, ":memory:")
				defer f.store.Close()
				ctx := context.Background()
				task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "job"})
				if err != nil {
					t.Fatal(err)
				}
				call := func() (TaskClaim, error) {
					return f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "operation")
				}
				if method == "renew" {
					c, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
					if err != nil {
						t.Fatal(err)
					}
					call = func() (TaskClaim, error) {
						return f.svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion, time.Minute, "operation")
					}
				}
				c, err := call()
				if err != nil {
					t.Fatal(err)
				}
				switch transition {
				case "completed":
					_, err = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion)
				case "released":
					_, err = f.svc.ReleaseTask(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion)
				case "expired":
					*f.now = f.now.Add(2 * time.Minute)
				case "reclaimed":
					*f.now = f.now.Add(2 * time.Minute)
					peer := claimWorker(t, f, f.context.ID)
					_, err = f.svc.ClaimTask(ctx, peer.Token, task.ID, time.Minute)
				case "renewed":
					_, err = f.svc.RenewClaim(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion, time.Minute)
				}
				if err != nil {
					t.Fatal(err)
				}
				var before Task
				if err = f.store.View(ctx, func(r ReadStore) error { var err error; before, err = r.Task(task.ID); return err }); err != nil {
					t.Fatal(err)
				}
				retry, err := call()
				if !errors.Is(err, ErrConflict) || retry.Token != "" {
					t.Fatalf("stale receipt minted token or missed conflict: %v", err)
				}
				var after Task
				if err = f.store.View(ctx, func(r ReadStore) error { var err error; after, err = r.Task(task.ID); return err }); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, after) {
					t.Fatal("stale receipt changed task/lease hash")
				}
			})
		}
	}
}

func TestTaskReceiptRecoveryRollback(t *testing.T) {
	for _, method := range []string{"claim", "renew"} {
		t.Run(method, func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			ctx := context.Background()
			task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "job"})
			if err != nil {
				t.Fatal(err)
			}
			call := func() (TaskClaim, error) {
				return f.svc.ClaimTaskWithOperation(ctx, f.admin.Token, task.ID, time.Minute, "operation")
			}
			if method == "renew" {
				c, err := f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				call = func() (TaskClaim, error) {
					return f.svc.RenewClaimWithOperation(ctx, f.admin.Token, task.ID, c.Token, c.Task.LeaseVersion, time.Minute, "operation")
				}
			}
			original, err := call()
			if err != nil {
				t.Fatal(err)
			}
			if err = f.store.Update(ctx, func(w WriteStore) error {
				_, err := w.(*sqlStore).q.Exec("CREATE TRIGGER fail_recovery BEFORE UPDATE OF lease_token_hash ON tasks BEGIN SELECT RAISE(ABORT,'recovery failure'); END")
				return err
			}); err != nil {
				t.Fatal(err)
			}
			recovered, err := call()
			if err == nil || recovered.Token != "" || recovered.Task.ID != "" {
				t.Fatal("failed recovery exposed a token/result")
			}
			if err = f.store.Update(ctx, func(w WriteStore) error { _, err := w.(*sqlStore).q.Exec("DROP TRIGGER fail_recovery"); return err }); err != nil {
				t.Fatal(err)
			}
			if _, err = f.svc.CompleteTask(ctx, f.admin.Token, task.ID, original.Token, original.Task.LeaseVersion); err != nil {
				t.Fatalf("recovery hash escaped rollback: %v", err)
			}
		})
	}
}
