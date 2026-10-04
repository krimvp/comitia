package main

import (
	"fmt"
	"path/filepath"
	"testing"
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
