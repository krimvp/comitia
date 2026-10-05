package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The helper commits state, returns its real claim through a pipe, then blocks.
// The parent kills only that child and recovers without a clean database close.
func TestCrashRecoveryCommittedState(t *testing.T) {
	if os.Getenv("COMITIA_CRASH_HELPER") == "1" {
		path, token, contextID, taskID := os.Getenv("COMITIA_DB"), os.Getenv("COMITIA_TOKEN"), os.Getenv("COMITIA_CONTEXT"), os.Getenv("COMITIA_TASK")
		st, err := OpenSQLite(path)
		if err != nil {
			os.Exit(11)
		}
		svc, err := New(Config{Store: st, Clock: func() time.Time {
			return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
		}})
		if err != nil {
			os.Exit(12)
		}
		if _, err = svc.AppendMessage(context.Background(), token, contextID, MessageInput{DedupeKey: "crash-msg", Body: "durable"}); err != nil {
			os.Exit(13)
		}
		if err = svc.AckMessages(context.Background(), token, contextID, 1); err != nil {
			os.Exit(14)
		}
		claim, err := svc.ClaimTask(context.Background(), token, taskID, time.Hour)
		if err != nil {
			os.Exit(15)
		}
		if err = json.NewEncoder(os.Stdout).Encode(claim); err != nil {
			os.Exit(16)
		}
		var b [1]byte
		_, _ = os.Stdin.Read(b[:])
		t.Fatal("helper unexpectedly unblocked")
		return
	}

	path := filepath.Join(t.TempDir(), "state.db")
	f := setup(t, path)
	task, err := f.svc.CreateTask(context.Background(), f.admin.Token, f.context.ID, TaskInput{Title: "lease"})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.store.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashRecoveryCommittedState$")
	cmd.Env = append(os.Environ(), "COMITIA_CRASH_HELPER=1", "COMITIA_DB="+path, "COMITIA_TOKEN="+f.admin.Token, "COMITIA_CONTEXT="+f.context.ID, "COMITIA_TASK="+task.ID)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	var oldClaim TaskClaim
	if err = json.NewDecoder(stdout).Decode(&oldClaim); err != nil {
		t.Fatalf("readiness: %v", err)
	}
	if oldClaim.Task.ID != task.ID || oldClaim.Token == "" {
		t.Fatal("invalid helper claim")
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	waited = true
	if err == nil {
		t.Fatal("helper exited cleanly")
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
	msgs, err := svc.ReplayMessages(context.Background(), f.admin.Token, f.context.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 || msgs[1].Body != "durable" || msgs[0].Envelope.Schema != TaskEventSchema || msgs[2].Envelope.Schema != TaskEventSchema {
		t.Fatalf("messages after crash: %#v", msgs)
	}
	cursor, err := svc.MessageCursor(context.Background(), f.admin.Token, f.context.ID)
	if err != nil || cursor != 1 {
		t.Fatalf("cursor=%d err=%v", cursor, err)
	}
	old, err := svc.GetTask(context.Background(), f.admin.Token, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != TaskLeased || old.LeaseVersion != oldClaim.Task.LeaseVersion || old.LeaseOwnerSessionID != oldClaim.Task.LeaseOwnerSessionID {
		t.Fatalf("lease recovery: %+v", old)
	}
	if _, err = svc.ClaimTask(context.Background(), f.admin.Token, task.ID, time.Hour); !errors.Is(err, ErrConflict) {
		t.Fatalf("active claim lost: %v", err)
	}
	*f.now = f.now.Add(2 * time.Hour)
	fresh, err := svc.ClaimTask(context.Background(), f.admin.Token, task.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CompleteTask(context.Background(), f.admin.Token, task.ID, oldClaim.Token, oldClaim.Task.LeaseVersion); !errors.Is(err, ErrStaleClaim) {
		t.Fatalf("stale completion: %v", err)
	}
	if _, err = svc.CompleteTask(context.Background(), f.admin.Token, task.ID, fresh.Token, fresh.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
}
