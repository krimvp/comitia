package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

func TestCLITaskRecoveryAndFence(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			base, cid := instanceCommandSetup(t, remote)
			run := func(args ...string) map[string]any { t.Helper(); return command(t, append(base, args...)...) }
			args := []string{"task-create", "--context-id", cid, "--title", "job", "--operation-id", "create"}
			task := run(args...)
			if again := run(args...); !reflect.DeepEqual(task, again) {
				t.Fatalf("create retry: %v", again)
			}
			tid := task["ID"].(string)
			args = []string{"task-claim", "--task-id", tid, "--operation-id", "claim"}
			claim := run(args...)
			again := run(args...)
			if !reflect.DeepEqual(claim["Task"], again["Task"]) || again["Token"] == "" || again["Token"] == claim["Token"] {
				t.Fatal("claim retry must rotate only token")
			}
			claim = again
			args = []string{"task-renew", "--task-id", tid, "--claim-token", claim["Token"].(string), "--lease-version", fmt.Sprint(claim["Task"].(map[string]any)["LeaseVersion"]), "--operation-id", "renew"}
			renewed := run(args...)
			again = run(args...)
			if !reflect.DeepEqual(renewed["Task"], again["Task"]) || again["Token"] == "" || again["Token"] == renewed["Token"] {
				t.Fatal("renew retry must rotate only token")
			}
			renewed = again
			token := renewed["Token"].(string)
			version := fmt.Sprint(renewed["Task"].(map[string]any)["LeaseVersion"])
			path := filepath.Join(t.TempDir(), "envelope.json")
			raw, _ := json.Marshal(map[string]any{"schema": "comitia.discussion.v1", "task_id": tid, "payload": map[string]any{}})
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			args = []string{"send", "--context-id", cid, "--dedupe-key", "result", "--body", "done", "--envelope-file", path, "--claim-token", token, "--lease-version", version}
			msg := run(args...)
			if msg["task_attempt"] == nil {
				t.Fatalf("fenced CLI result: %v", msg)
			}
			run("task-complete", "--task-id", tid, "--claim-token", token, "--lease-version", version)
			args[4] = "stale"
			if err := commandErr(t, append(base, args...)...); !errors.Is(err, comitia.ErrStaleClaim) {
				t.Fatalf("stale CLI result: %v", err)
			}
		})
	}
}
