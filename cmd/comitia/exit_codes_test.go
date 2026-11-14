package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

// cli runs main in a subprocess and returns stdout, stderr and the exit code.
func cli(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	process := exec.Command(os.Args[0], append([]string{"-test.run=^TestCLIHelpProcess$", "--"}, args...)...)
	process.Env = append(os.Environ(), "COMITIA_TEST_HELP_PROCESS=1", "COMITIA_SERVER_URL=", "COMITIA_CREDENTIAL=")
	var stdout, stderr strings.Builder
	process.Stdout, process.Stderr = &stdout, &stderr
	err := process.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return stdout.String(), stderr.String(), code
}

func TestExitCodesDistinguishErrorClasses(t *testing.T) {
	db := filepath.Join(t.TempDir(), "exit.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	owner := boot["credential"].(map[string]any)["Token"].(string)
	w := command(t, "--db", db, "--credential", owner, "workspace-create", "--name", "w")
	c := command(t, "--db", db, "--credential", owner, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")
	other := command(t, "--db", db, "--credential", owner, "context-create", "--workspace-id", w["ID"].(string), "--name", "other")
	d := command(t, "--db", db, "--credential", owner, "delegate", "--context-id", c["ID"].(string))
	worker := d["credential"].(map[string]any)["Token"].(string)
	task := command(t, "--db", db, "--credential", owner, "task-create", "--context-id", c["ID"].(string), "--title", "t")
	command(t, "--db", db, "--credential", owner, "task-claim", "--task-id", task["ID"].(string))
	for name, tc := range map[string]struct {
		args []string
		code int
	}{
		"unknown flag":     {[]string{"replay", "--bogus"}, exitUsage},
		"missing flag":     {[]string{"replay"}, exitUsage},
		"unknown command":  {[]string{"bogus"}, exitUsage},
		"unauthenticated":  {[]string{"--credential", "ct_wrong", "context-list"}, exitUnauthenticated},
		"forbidden":        {[]string{"--credential", worker, "replay", "--context-id", other["ID"].(string)}, exitForbidden},
		"conflict":         {[]string{"--credential", owner, "task-claim", "--task-id", task["ID"].(string)}, exitConflict},
		"bootstrap used":   {[]string{"--bootstrap-credential", "once", "bootstrap"}, exitConflict},
		"global bad value": {[]string{"--bogus", "replay"}, exitUsage},
	} {
		t.Run(name, func(t *testing.T) {
			_, stderr, code := cli(t, append([]string{"--db", db}, tc.args...)...)
			if code != tc.code {
				t.Fatalf("exit %d, want %d: %s", code, tc.code, stderr)
			}
			// stderr is exactly one JSON error line, with no usage text before it.
			var e map[string]string
			if strings.Count(stderr, "\n") != 1 || json.Unmarshal([]byte(stderr), &e) != nil || e["error"] == "" {
				t.Fatalf("stderr=%q", stderr)
			}
		})
	}
}

func TestExitCodeClassifiesTransportFailures(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	err := run(context.Background(), []string{"--server-url", ts.URL, "--credential", "x", "replay", "--context-id", "c"})
	if exitCode(err) != exitUnavailable {
		t.Fatalf("503: %v -> %d", err, exitCode(err))
	}
	ts.Close()
	err = run(context.Background(), []string{"--server-url", ts.URL, "--credential", "x", "replay", "--context-id", "c"})
	if exitCode(err) != exitUnavailable {
		t.Fatalf("refused: %v -> %d", err, exitCode(err))
	}
	if exitCode(fmt.Errorf("wrapped: %w", context.DeadlineExceeded)) != exitUnavailable || exitCode(errors.New("boom")) != exitInternal || exitCode(comitia.ErrStaleClaim) != exitConflict || exitCode(fmt.Errorf("%w: x", comitia.ErrNotFound)) != exitNotFound {
		t.Fatal("class mapping")
	}
}

func TestVersionAndHelpCommands(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"--version"}} {
		stdout, stderr, code := cli(t, args...)
		var build comitia.BuildInfo
		// The test harness appends PASS after main returns.
		if code != 0 || json.NewDecoder(strings.NewReader(stdout)).Decode(&build) != nil || build != comitia.BuildIdentity() {
			t.Fatalf("%v: exit %d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
	_, want, code := cli(t, "--help")
	_, got, helpCode := cli(t, "help")
	if code != 0 || helpCode != 0 || got != want || !strings.Contains(got, "Usage") {
		t.Fatalf("help exit %d/%d:\n%s\n---\n%s", code, helpCode, want, got)
	}
}
