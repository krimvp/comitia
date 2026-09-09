package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func commandAny(t *testing.T, args ...string) any {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	err = run(context.Background(), args)
	_ = w.Close()
	os.Stdout = old
	b, readErr := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatalf("run %v: %v", args, err)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	var result any
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("invalid JSON %q: %v", b, err)
	}
	return result
}
func command(t *testing.T, args ...string) map[string]any {
	t.Helper()
	return commandAny(t, args...).(map[string]any)
}

func commandErr(t *testing.T, args ...string) error {
	t.Helper()
	return run(context.Background(), args)
}

func TestTaskGetListCommands(t *testing.T) {
	db := filepath.Join(t.TempDir(), "tasks.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	cred := boot["credential"].(map[string]any)["Token"].(string)
	w := command(t, "--db", db, "--credential", cred, "workspace-create", "--name", "w")
	c := command(t, "--db", db, "--credential", cred, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")
	cid := c["ID"].(string)
	task := command(t, "--db", db, "--credential", cred, "task-create", "--context-id", cid, "--title", "one", "--body", "b")
	id := task["ID"].(string)
	got := command(t, "--db", db, "--credential", cred, "task-get", "--task-id", id)
	if got["ID"] != id {
		t.Fatalf("get=%v", got)
	}
	list := commandAny(t, "--db", db, "--credential", cred, "task-list", "--context-id", cid).([]any)
	if len(list) != 1 {
		t.Fatalf("list=%v", list)
	}
	filtered := commandAny(t, "--db", db, "--credential", cred, "task-list", "--context-id", cid, "--status", "completed").([]any)
	if len(filtered) != 0 {
		t.Fatalf("filtered=%v", filtered)
	}
	c2 := command(t, "--db", db, "--credential", cred, "context-create", "--workspace-id", w["ID"].(string), "--name", "other")
	d := command(t, "--db", db, "--credential", cred, "delegate", "--name", "reader", "--context-id", c2["ID"].(string), "--permissions", "read")
	reader := d["credential"].(map[string]any)["Token"].(string)
	if err := commandErr(t, "--db", db, "--credential", reader, "task-get", "--task-id", id); err == nil {
		t.Fatal("cross-context get unexpectedly allowed")
	}
	if err := commandErr(t, "--db", db, "--credential", reader, "task-list", "--context-id", cid); err == nil {
		t.Fatal("cross-context list unexpectedly allowed")
	}
	for _, a := range [][]string{{"task-get"}, {"task-list", "--context-id", cid, "--status", "bogus"}, {"task-list", "--context-id", cid, "--limit", "0"}, {"task-list", "--context-id", cid, "--limit", "1001"}, {"task-list"}} {
		if err := commandErr(t, append([]string{"--db", db, "--credential", cred}, a...)...); err == nil {
			t.Fatalf("expected error for %v", a)
		}
	}
}

func TestEmbeddedCommandsShareDurableState(t *testing.T) {
	db := filepath.Join(t.TempDir(), "comitia.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "offline-once", "bootstrap", "--name", "owner")
	owner := boot["credential"].(map[string]any)["Token"].(string)
	workspace := command(t, "--db", db, "--credential", owner, "workspace-create", "--name", "demo")
	contextValue := command(t, "--db", db, "--credential", owner, "context-create", "--workspace-id", workspace["ID"].(string), "--name", "shared")
	contextID := contextValue["ID"].(string)
	delegated := command(t, "--db", db, "--credential", owner, "delegate", "--name", "worker", "--context-id", contextID, "--permissions", "read,write,claim")
	worker := delegated["credential"].(map[string]any)["Token"].(string)
	message := command(t, "--db", db, "--credential", worker, "send", "--context-id", contextID, "--dedupe-key", "worker-1", "--body", "ready")
	if message["Body"] != "ready" {
		t.Fatalf("message=%v", message)
	}
	replayed := commandAny(t, "--db", db, "--credential", owner, "replay", "--context-id", contextID).([]any)
	if len(replayed) != 1 || replayed[0].(map[string]any)["Body"] != "ready" {
		t.Fatalf("messages=%v", replayed)
	}
	command(t, "--db", db, "--credential", owner, "ack", "--context-id", contextID, "--sequence", "1")
	cursor := command(t, "--db", db, "--credential", owner, "cursor", "--context-id", contextID)
	if cursor["sequence"] != float64(1) {
		t.Fatalf("cursor=%v", cursor)
	}
}

func TestRemoteFailureDoesNotCreateEmbeddedDatabase(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer ts.Close()
	db := filepath.Join(t.TempDir(), "must-not-exist.db")
	if err := run(context.Background(), []string{"--db", db, "--server-url", ts.URL, "--credential", "x", "replay", "--context-id", "c"}); err == nil {
		t.Fatal("expected remote error")
	}
	if _, err := os.Stat(db); !os.IsNotExist(err) {
		t.Fatalf("embedded database was created: %v", err)
	}
}
