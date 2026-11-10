package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krimvp/comitia"
)

// A missing required flag is named before storage opens, instead of surfacing
// as the server's forbidden or not-found answer for an empty ID.
func TestEveryCommandNamesMissingRequiredFlag(t *testing.T) {
	t.Setenv("COMITIA_BOOTSTRAP_CREDENTIAL", "")
	required := map[string]string{
		"bootstrap": "--bootstrap-credential", "delegate": "--context-id or --grant", "revoke": "--session-id",
		"grant": "--session-id", "grant-revoke": "--grant-id", "credential-check": "--context-id",
		"workspace-create": "--name", "context-create": "--name", "context-get": "--context-id",
		"context-children": "--parent-id", "context-archive": "--context-id", "reference-add": "--context-id",
		"send": "--context-id", "replay": "--context-id", "wait": "--context-id", "message-get": "--context-id",
		"watch": "--context-id", "ack": "--context-id", "cursor": "--context-id", "participants": "--context-id",
		"instances": "--context-id", "instance-resume": "--context-id", "task-create": "--context-id",
		"task-get": "--task-id", "task-list": "--context-id", "task-claim": "--task-id", "task-recover": "--task-id",
		"task-renew": "--task-id", "task-complete": "--task-id", "task-release": "--task-id", "task-fail": "--task-id",
		"task-cancel": "--task-id", "mcp-http": "--server-url",
	}
	optional := map[string]bool{"context-list": true, "server-info": true, "storage-status": true, "serve": true, "mcp": true}
	for _, cmd := range usageCommands(t) {
		t.Run(cmd, func(t *testing.T) {
			want, ok := required[cmd]
			if !ok {
				if !optional[cmd] {
					t.Fatal("classify this command's required flags")
				}
				return
			}
			db := filepath.Join(t.TempDir(), "required.db")
			err := run(context.Background(), []string{"--db", db, "--server-url=", cmd})
			if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), cmd+" requires "+want) {
				t.Fatalf("err=%v", err)
			}
			if _, err := os.Stat(db); !os.IsNotExist(err) {
				t.Fatalf("rejected command touched storage: %v", err)
			}
		})
	}
}

func TestLaterRequiredFlagsAreNamedToo(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"send", "--context-id", "c"}, "send requires --dedupe-key"},
		{[]string{"send", "--context-id", "c", "--dedupe-key", "k"}, "send requires --body or --body-file or --envelope-file"},
		{[]string{"ack", "--context-id", "c"}, "ack requires --sequence"},
		{[]string{"context-create", "--name", "n"}, "context-create requires --workspace-id or --parent-id"},
		{[]string{"reference-add", "--context-id", "c", "--kind", "k"}, "reference-add requires --uri"},
		{[]string{"task-renew", "--task-id", "t", "--claim-token", "x"}, "task-renew requires --lease-version"},
		{[]string{"task-complete", "--task-id", "t", "--lease-version", "1"}, "task-complete requires --claim-token"},
		{[]string{"task-fail", "--task-id", "t", "--lease-version", "1", "--reason", "r"}, "task-fail requires --claim-token"},
		{[]string{"task-cancel", "--task-id", "t", "--lease-version", "1"}, "task-cancel requires --reason"},
		{[]string{"instance-resume", "--context-id", "c"}, "instance-resume requires --resume-file"},
		{[]string{"context-get", "--context-id="}, "context-get requires --context-id"},
	} {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "required.db")
			err := run(context.Background(), append([]string{"--db", db, "--server-url="}, tc.args...))
			if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v", err)
			}
			if _, err := os.Stat(db); !os.IsNotExist(err) {
				t.Fatalf("rejected command touched storage: %v", err)
			}
		})
	}
}

func TestHolderCancelWithoutClaimTokenNamesTheFlag(t *testing.T) {
	db := filepath.Join(t.TempDir(), "cancel.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	owner := boot["credential"].(map[string]any)["Token"].(string)
	base := []string{"--db", db, "--credential", owner}
	w := command(t, append(base, "workspace-create", "--name", "w")...)
	c := command(t, append(base, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")...)
	cid := c["ID"].(string)
	d := command(t, append(base, "delegate", "--name", "worker", "--context-id", cid)...)
	worker := []string{"--db", db, "--credential", d["credential"].(map[string]any)["Token"].(string)}
	task := command(t, append(base, "task-create", "--context-id", cid, "--title", "t")...)
	claim := command(t, append(worker, "task-claim", "--task-id", task["ID"].(string))...)
	err := commandErr(t, append(worker, "task-cancel", "--task-id", task["ID"].(string), "--lease-version", "1", "--reason", "stop")...)
	if !errors.Is(err, comitia.ErrStaleClaim) || !strings.Contains(err.Error(), "--claim-token") {
		t.Fatalf("err=%v", err)
	}
	command(t, append(worker, "task-cancel", "--task-id", task["ID"].(string), "--lease-version", "1", "--reason", "stop", "--claim-token", claim["Token"].(string))...)
}

func TestClaimLeaseErrorNamesOnlyTheLease(t *testing.T) {
	db := filepath.Join(t.TempDir(), "lease.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	base := []string{"--db", db, "--credential", boot["credential"].(map[string]any)["Token"].(string)}
	w := command(t, append(base, "workspace-create", "--name", "w")...)
	c := command(t, append(base, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")...)
	task := command(t, append(base, "task-create", "--context-id", c["ID"].(string), "--title", "t")...)
	err := commandErr(t, append(base, "task-claim", "--task-id", task["ID"].(string), "--lease", "0s")...)
	if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "lease") || strings.Contains(err.Error(), "agent_instance") {
		t.Fatalf("err=%v", err)
	}
}
