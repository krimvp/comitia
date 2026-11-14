package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

func TestTaskClaimInstanceAndRelease(t *testing.T) {
	db := filepath.Join(t.TempDir(), "claims.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	cred := boot["credential"].(map[string]any)["Token"].(string)
	w := command(t, "--db", db, "--credential", cred, "workspace-create", "--name", "w")
	c := command(t, "--db", db, "--credential", cred, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")
	task := command(t, "--db", db, "--credential", cred, "task-create", "--context-id", c["ID"].(string), "--title", "one")
	id := task["ID"].(string)
	claim := command(t, "--db", db, "--credential", cred, "task-claim", "--task-id", id, "--open-ended", "--agent-instance", "cli-1", "--agent-instance-label", "CLI")
	held := claim["Task"].(map[string]any)
	instance, _ := held["lease_owner_instance"].(map[string]any)
	if instance["id"] != "cli-1" || instance["label"] != "CLI" {
		t.Fatalf("claim=%v", claim)
	}
	got := command(t, "--db", db, "--credential", cred, "task-get", "--task-id", id)
	if got["lease_owner_instance"].(map[string]any)["id"] != "cli-1" {
		t.Fatalf("get=%v", got)
	}
	version := fmt.Sprint(held["LeaseVersion"])
	if err := commandErr(t, "--db", db, "--credential", cred, "task-release", "--lease-version", version); err == nil {
		t.Fatal("release without task-id")
	}
	released := command(t, "--db", db, "--credential", cred, "task-release", "--task-id", id, "--claim-token", claim["Token"].(string), "--lease-version", version)
	if released["Status"] != "pending" || released["lease_owner_instance"] != nil {
		t.Fatalf("released=%v", released)
	}
	if err := commandErr(t, "--db", db, "--credential", cred, "task-claim", "--task-id", id, "--agent-instance", "bad id"); err == nil {
		t.Fatal("invalid instance accepted")
	}
}

func TestTaskClaimInstanceDefaults(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			base, cid := instanceCommandSetup(t, remote)
			for _, tc := range []struct {
				name, id, label string
				flags           []string
				want            *comitia.AgentInstance
				invalid         bool
			}{
				{name: "legacy"},
				{name: "default", id: "worker", label: "Worker", want: &comitia.AgentInstance{ID: "worker", Label: "Worker"}},
				{name: "explicit", id: "worker", label: "Worker", flags: []string{"--agent-instance", "explicit"}, want: &comitia.AgentInstance{ID: "explicit"}},
				{name: "label", id: "worker", label: "Worker", flags: []string{"--agent-instance-label", "Override"}, want: &comitia.AgentInstance{ID: "worker", Label: "Override"}},
				{name: "empty-label", id: "worker", label: "Worker", flags: []string{"--agent-instance-label", ""}, want: &comitia.AgentInstance{ID: "worker"}},
				{name: "empty-id", id: "worker", flags: []string{"--agent-instance", ""}, invalid: true},
				{name: "empty-id-no-default", flags: []string{"--agent-instance", ""}, invalid: true},
				{name: "label-no-default", flags: []string{"--agent-instance-label", "Override"}, invalid: true},
				{name: "invalid", id: "worker", flags: []string{"--agent-instance", "bad id"}, invalid: true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					t.Setenv("COMITIA_AGENT_INSTANCE", tc.id)
					t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", tc.label)
					task := command(t, append(base, "task-create", "--context-id", cid, "--title", tc.name)...)
					args := append(base, "task-claim", "--task-id", task["ID"].(string))
					args = append(args, tc.flags...)
					if tc.invalid {
						if err := commandErr(t, args...); !errors.Is(err, comitia.ErrInvalid) {
							t.Fatalf("claim: %v", err)
						}
						return
					}
					claim := command(t, args...)
					held := claim["Task"].(map[string]any)
					instance, _ := held["lease_owner_instance"].(map[string]any)
					if tc.want == nil {
						if instance != nil {
							t.Fatalf("legacy claim: %v", claim)
						}
					} else if instance["id"] != tc.want.ID || (tc.want.Label == "" && instance["label"] != nil) || (tc.want.Label != "" && instance["label"] != tc.want.Label) {
						t.Fatalf("claim: %v want=%v", claim, tc.want)
					}
				})
			}
		})
	}
}
