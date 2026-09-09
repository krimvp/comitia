package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvelopeCommands(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "state.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "local", "bootstrap")
	token := boot["credential"].(map[string]any)["Token"].(string)
	w := command(t, "--db", db, "--credential", token, "workspace-create", "--name", "w")
	c := command(t, "--db", db, "--credential", token, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")
	cid := c["ID"].(string)
	ps := commandAny(t, "--db", db, "--credential", token, "participants", "--context-id", cid).([]any)
	if len(ps) != 1 {
		t.Fatalf("participants: %v", ps)
	}
	path := filepath.Join(dir, "envelope.json")
	if err := os.WriteFile(path, []byte(`{"schema":"comitia.discussion.v1","payload":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := command(t, "--db", db, "--credential", token, "send", "--context-id", cid, "--dedupe-key", "typed", "--body", "hello", "--envelope-file", path)
	if m["envelope"].(map[string]any)["schema"] != "comitia.discussion.v1" {
		t.Fatal("envelope lost")
	}
	if err := os.WriteFile(path, []byte(`{"schema":"comitia.discussion.v1","from":"fake","payload":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEnvelope(path); err == nil {
		t.Fatal("unknown envelope field accepted")
	}
}
