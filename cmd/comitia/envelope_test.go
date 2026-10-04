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
	if err := os.WriteFile(path, []byte(`{"schema":"comitia.discussion.v1","recipient_instances":["reviewer"],"payload":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	m := command(t, "--db", db, "--credential", token, "send", "--context-id", cid, "--dedupe-key", "typed", "--body", "hello", "--envelope-file", path, "--agent-instance", "worker", "--agent-instance-label", "CLI worker")
	if m["envelope"].(map[string]any)["schema"] != "comitia.discussion.v1" {
		t.Fatal("envelope lost")
	}
	replay := commandAny(t, "--db", db, "--credential", token, "replay", "--context-id", cid).([]any)
	claim := replay[0].(map[string]any)
	if claim["agent_instance"].(map[string]any)["id"] != "worker" || claim["agent_instance"].(map[string]any)["label"] != "CLI worker" || claim["agent_instance_self_reported"] != true {
		t.Fatalf("CLI lost claim: %v", claim)
	}
	command(t, "--db", db, "--credential", token, "ack", "--context-id", cid, "--sequence", "1", "--agent-instance", "worker")
	for _, check := range []struct {
		instance string
		want     float64
	}{{"worker", 1}, {"reviewer", 0}, {"", 0}} {
		cursor := command(t, "--db", db, "--credential", token, "cursor", "--context-id", cid, "--agent-instance", check.instance)
		if cursor["sequence"] != check.want {
			t.Fatalf("CLI cursor %q: %v", check.instance, cursor)
		}
	}
	if err := os.WriteFile(path, []byte(`{"schema":"comitia.discussion.v1","from":"fake","payload":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readEnvelope(path); err == nil {
		t.Fatal("unknown envelope field accepted")
	}
}
