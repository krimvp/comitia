package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestBootstrapRemoteIsUsageError: bootstrap with --server-url is invalid
// input, like the other commands available only with embedded SQLite.
func TestBootstrapRemoteIsUsageError(t *testing.T) {
	_, stderr, code := cli(t, "--server-url", "http://127.0.0.1:1", "--bootstrap-credential", "once", "bootstrap")
	var e map[string]string
	if code != exitUsage || json.Unmarshal([]byte(stderr), &e) != nil || e["error"] != "invalid input: bootstrap is available only with embedded SQLite" {
		t.Fatalf("exit %d, want %d: %s", code, exitUsage, stderr)
	}
}

// TestTaskListLimitCheckedBeforeStorage: a bad --limit fails as input before
// the command looks for the database or the server.
func TestTaskListLimitCheckedBeforeStorage(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent", "missing.db")
	for _, mode := range [][]string{{"--db", missing}, {"--server-url", "http://127.0.0.1:1"}} {
		for _, limit := range []string{"0", "1001", "-1"} {
			args := append(append([]string{}, mode...), "--credential", "x", "task-list", "--context-id", "c", "--limit", limit)
			_, stderr, code := cli(t, args...)
			var e map[string]string
			if code != exitUsage || json.Unmarshal([]byte(stderr), &e) != nil || e["error"] != "invalid input: limit must be between 1 and 1000" {
				t.Fatalf("%v: exit %d: %s", args, code, stderr)
			}
		}
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatalf("database directory touched: %v", err)
	}
}
