package main

import (
	"path/filepath"
	"testing"
)

func TestChildContextCommands(t *testing.T) {
	db := filepath.Join(t.TempDir(), "children.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	cred := boot["credential"].(map[string]any)["Token"].(string)
	w := command(t, "--db", db, "--credential", cred, "workspace-create", "--name", "w")
	epic := command(t, "--db", db, "--credential", cred, "context-create", "--workspace-id", w["ID"].(string), "--name", "epic")["ID"].(string)
	task := command(t, "--db", db, "--credential", cred, "task-create", "--context-id", epic, "--title", "task 15")["ID"].(string)
	d := command(t, "--db", db, "--credential", cred, "delegate", "--name", "coord", "--context-id", epic, "--permissions", "read,write,create")
	coord := d["credential"].(map[string]any)["Token"].(string)
	child := command(t, "--db", db, "--credential", coord, "context-create", "--parent-id", epic, "--task-id", task, "--name", "task-15", "--parent-readable")
	if child["ParentContextID"] != epic || child["TaskID"] != task || child["ParentReadable"] != true {
		t.Fatalf("child=%v", child)
	}
	command(t, "--db", db, "--credential", coord, "send", "--context-id", child["ID"].(string), "--dedupe-key", "k", "--body", "hi")
	children := commandAny(t, "--db", db, "--credential", cred, "context-children", "--parent-id", epic).([]any)
	if len(children) != 1 || children[0].(map[string]any)["ID"] != child["ID"] {
		t.Fatalf("children=%v", children)
	}
	listed := commandAny(t, "--db", db, "--credential", coord, "context-list").([]any)
	found := false
	for _, c := range listed {
		m := c.(map[string]any)
		if m["ID"] == child["ID"] {
			found = m["ParentContextID"] == epic && m["TaskID"] == task
		}
	}
	if !found {
		t.Fatalf("context-list=%v", listed)
	}
	for _, a := range [][]string{
		{"context-create", "--parent-id", epic, "--workspace-id", w["ID"].(string), "--name", "x"},
		{"context-create", "--workspace-id", w["ID"].(string), "--task-id", task, "--name", "x"},
		{"context-create", "--workspace-id", w["ID"].(string), "--parent-readable", "--name", "x"},
		{"context-children"},
	} {
		if err := commandErr(t, append([]string{"--db", db, "--credential", coord}, a...)...); err == nil {
			t.Fatalf("expected error for %v", a)
		}
	}
	reader := command(t, "--db", db, "--credential", cred, "delegate", "--name", "reader", "--context-id", epic, "--permissions", "read,write")["credential"].(map[string]any)["Token"].(string)
	if err := commandErr(t, "--db", db, "--credential", reader, "context-create", "--parent-id", epic, "--name", "x"); err == nil {
		t.Fatal("child creation without create unexpectedly allowed")
	}
}
