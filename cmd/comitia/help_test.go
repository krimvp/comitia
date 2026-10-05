package main

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIHelpProcess(t *testing.T) {
	if os.Getenv("COMITIA_TEST_HELP_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"comitia"}, os.Args[i+1:]...)
			main()
			return
		}
	}
	t.Fatal("missing command arguments")
}

func TestHelpSucceedsBeforeOpeningStorageOrValidatingRuntime(t *testing.T) {
	commands := strings.Fields("server-info bootstrap delegate revoke workspace-create context-create context-list context-get context-children context-archive reference-add grant grant-revoke credential-check send participants replay wait watch ack cursor instances instance-resume task-create task-get task-list task-claim task-renew task-complete task-release task-fail task-cancel serve mcp mcp-http")
	for _, command := range append([]string{""}, commands...) {
		t.Run(command, func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "help.db")
			args := []string{"--db", db}
			if command != "" {
				args = append(args, command)
			}
			args = append(args, "--help")
			process := exec.Command(os.Args[0], append([]string{"-test.run=^TestCLIHelpProcess$", "--"}, args...)...)
			process.Env = append(os.Environ(), "COMITIA_TEST_HELP_PROCESS=1", "COMITIA_SERVER_URL=invalid-url", "COMITIA_AGENT_INSTANCE=invalid instance", "COMITIA_CREDENTIAL=help-secret", "COMITIA_BOOTSTRAP_CREDENTIAL=bootstrap-secret", "HOME="+dir)
			output, err := process.CombinedOutput()
			if err != nil {
				t.Errorf("help exit: %v: %s", err, output)
			}
			if strings.Contains(string(output), "help-secret") || strings.Contains(string(output), "bootstrap-secret") || strings.Contains(string(output), `"error"`) {
				t.Errorf("help disclosed a secret or returned a JSON error: %s", output)
			}
			if !strings.Contains(string(output), "Usage") {
				t.Errorf("help missing usage: %s", output)
			}
			if _, err := os.Stat(db); !os.IsNotExist(err) {
				t.Errorf("help touched storage: %v", err)
			}
		})
	}
}

func TestHelpTokenAsFlagValueIsOrdinaryMessageData(t *testing.T) {
	db := filepath.Join(t.TempDir(), "messages.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	credential := boot["credential"].(map[string]any)["Token"].(string)
	base := []string{"--db", db, "--credential", credential}
	w := command(t, append(base, "workspace-create", "--name", "w")...)
	c := command(t, append(base, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")...)
	m := command(t, append(base, "send", "--context-id", c["ID"].(string), "--dedupe-key", "help-value", "--body", "--help")...)
	if m["Body"] != "--help" {
		t.Fatalf("message=%v", m)
	}
}

func TestCommandHelpDoesNotCreateOrOpenEmbeddedDatabase(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "existing"}[existing], func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "help.db")
			if existing {
				if err := os.WriteFile(db, []byte("preserve this invalid database"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := run(context.Background(), []string{"--db", db, "--server-url=", "send", "--body", "some text", "--help"})
			if err != nil {
				t.Errorf("help: %v", err)
			}
			if existing {
				data, err := os.ReadFile(db)
				if err != nil || string(data) != "preserve this invalid database" {
					t.Errorf("changed database: %q: %v", data, err)
				}
			} else if _, err := os.Stat(db); !os.IsNotExist(err) {
				t.Errorf("created database: %v", err)
			}
		})
	}
}

func TestGlobalHelpDescribesCommandGroups(t *testing.T) {
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	err = run(context.Background(), []string{"--help"})
	_ = w.Close()
	os.Stderr = old
	output, readErr := io.ReadAll(r)
	_ = r.Close()
	if err != nil || readErr != nil {
		t.Fatalf("help: %v / %v", err, readErr)
	}
	for _, text := range []string{"Sessions", "Contexts", "Messages", "Tasks", "Services", "bootstrap", "context-list", "send", "task-claim", "mcp-http", "<command> --help"} {
		if !strings.Contains(string(output), text) {
			t.Errorf("missing %q in %s", text, output)
		}
	}
}
