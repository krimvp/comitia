package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
)

func TestInstancePresenceCommands(t *testing.T) {
	for _, remote := range []bool{false, true} {
		name := "embedded"
		if remote {
			name = "remote"
		}
		t.Run(name, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "presence.db")
			boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
			credential := boot["credential"].(map[string]any)["Token"].(string)
			base := []string{"--db", db, "--credential", credential}
			w := command(t, append(base, "workspace-create", "--name", "w")...)
			c := command(t, append(base, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")...)
			cid := c["ID"].(string)
			if remote {
				api, closeFn, err := open(config{db: db})
				if err != nil {
					t.Fatal(err)
				}
				defer closeFn()
				server := httptest.NewServer(httpapi.NewHandler(api))
				defer server.Close()
				base = []string{"--server-url", server.URL, "--credential", credential}
			}
			rows := commandAny(t, append(base, "instances", "--context-id", cid)...).([]any)
			if len(rows) != 0 {
				t.Fatalf("initial: %v", rows)
			}
			file := filepath.Join(t.TempDir(), "resume.json")
			if err := os.WriteFile(file, []byte(`{"harness":"codex","cwd":"/tmp/work","pid":123}`), 0600); err != nil {
				t.Fatal(err)
			}
			command(t, append(base, "instance-resume", "--context-id", cid, "--agent-instance", "worker", "--agent-instance-label", "CLI worker", "--resume-file", file)...)
			command(t, append(base, "send", "--context-id", cid, "--agent-instance", "worker", "--agent-instance-label", "CLI worker", "--dedupe-key", "x", "--body", "x")...)
			commandAny(t, append(base, "replay", "--context-id", cid, "--agent-instance", "worker")...)
			command(t, append(base, "cursor", "--context-id", cid, "--agent-instance", "worker")...)
			command(t, append(base, "ack", "--context-id", cid, "--agent-instance", "worker", "--sequence", "0")...)
			rows = commandAny(t, append(base, "instances", "--context-id", cid)...).([]any)
			if len(rows) != 1 {
				t.Fatalf("rows: %v", rows)
			}
			p := rows[0].(map[string]any)
			if p["id"] != "worker" || p["label"] != "CLI worker" || p["last_seen"] == nil || p["active"] != true || p["seen_seconds_ago"] == nil || p["recent"] != true || p["resume"].(map[string]any)["pid"] != float64(123) {
				t.Fatalf("presence: %v", p)
			}
			if err := os.WriteFile(file, []byte(`[]`), 0600); err != nil {
				t.Fatal(err)
			}
			if err := commandErr(t, append(base, "instance-resume", "--context-id", cid, "--agent-instance", "worker", "--resume-file", file)...); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("invalid resume: %v", err)
			}
			for _, invalid := range []string{`{"x":`, `{"x":"` + strings.Repeat("x", comitia.MaxInstanceResumeBytes) + `"}`} {
				if err := os.WriteFile(file, []byte(invalid), 0600); err != nil {
					t.Fatal(err)
				}
				if err := commandErr(t, append(base, "instance-resume", "--context-id", cid, "--agent-instance", "worker", "--resume-file", file)...); !errors.Is(err, comitia.ErrInvalid) {
					t.Fatalf("malformed/oversized resume: %v", err)
				}
			}
			if err := commandErr(t, append(base, "instances", "--context-id", "unreadable")...); !errors.Is(err, comitia.ErrForbidden) {
				t.Fatalf("scope: %v", err)
			}
		})
	}
}

func TestPresenceCommandDeclinesOtherDomains(t *testing.T) {
	if err := runPresence(context.Background(), config{}, nil, nil, "send", nil); err != errUnknownCommand {
		t.Fatal(err)
	}
}
