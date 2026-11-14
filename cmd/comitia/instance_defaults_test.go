package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/httpapi"
)

func instanceCommandSetup(t *testing.T, remote bool) ([]string, string) {
	t.Helper()
	t.Setenv("COMITIA_AGENT_INSTANCE", "")
	t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "")
	db := filepath.Join(t.TempDir(), "instances.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	token := boot["credential"].(map[string]any)["Token"].(string)
	base := []string{"--db", db, "--credential", token}
	w := command(t, append(base, "workspace-create", "--name", "w")...)
	c := command(t, append(base, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")...)
	if remote {
		api, closeFn, err := open(config{db: db})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(closeFn)
		ts := httptest.NewServer(httpapi.NewHandler(api))
		t.Cleanup(ts.Close)
		base = []string{"--server-url", ts.URL, "--credential", token}
	}
	return base, c["ID"].(string)
}

func TestCLIInstanceDefaults(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			base, cid := instanceCommandSetup(t, remote)
			t.Setenv("COMITIA_AGENT_INSTANCE", "worker")
			t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "Worker")
			send := append(base, "send", "--context-id", cid, "--dedupe-key", "started", "--body", "ready")
			a := command(t, send...)
			if instance := a["agent_instance"].(map[string]any); instance["id"] != "worker" || instance["label"] != "Worker" {
				t.Fatalf("default send: %v", a)
			}
			if retry := command(t, send...); retry["ID"] != a["ID"] || retry["Sequence"] != a["Sequence"] {
				t.Fatalf("retry: %v", retry)
			}
			explicit := command(t, append(send, "--agent-instance", "explicit")...)
			if instance := explicit["agent_instance"].(map[string]any); instance["id"] != "explicit" || instance["label"] != nil {
				t.Fatalf("explicit send inherited label: %v", explicit)
			}
			command(t, append(base, "ack", "--context-id", cid, "--sequence", "1")...)
			cursor := append(base, "cursor", "--context-id", cid)
			if got := command(t, cursor...); got["sequence"] != float64(1) {
				t.Fatalf("default cursor: %v", got)
			}
			if got := command(t, append(cursor, "--agent-instance", "")...); got["sequence"] != float64(0) {
				t.Fatalf("explicit legacy cursor: %v", got)
			}
			command(t, append(base, "ack", "--context-id", cid, "--sequence", "2", "--agent-instance", "explicit")...)
			if got := command(t, append(cursor, "--agent-instance", "explicit")...); got["sequence"] != float64(2) {
				t.Fatalf("explicit cursor: %v", got)
			}
			t.Setenv("COMITIA_AGENT_INSTANCE", "reviewer")
			t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "Reviewer")
			b := command(t, send...)
			if b["ID"] == a["ID"] || b["agent_instance"].(map[string]any)["id"] != "reviewer" {
				t.Fatalf("process defaults collided: %v", b)
			}
			if got := command(t, cursor...); got["sequence"] != float64(0) {
				t.Fatalf("separate default cursor: %v", got)
			}
			labelled := command(t, append(base, "send", "--context-id", cid, "--dedupe-key", "labelled", "--body", "ready", "--agent-instance-label", "Override")...)
			if instance := labelled["agent_instance"].(map[string]any); instance["id"] != "reviewer" || instance["label"] != "Override" {
				t.Fatalf("explicit label: %v", labelled)
			}
			if err := commandErr(t, append(send, "--agent-instance", "")...); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("empty explicit message instance: %v", err)
			}
		})
	}
}

func TestMCPCommandUsesInstanceEnvironment(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			base, cid := instanceCommandSetup(t, remote)
			t.Setenv("COMITIA_AGENT_INSTANCE", "worker")
			t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "Worker")
			input := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" + `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"send_message","arguments":{"context_id":"` + cid + `","dedupe_key":"started","body":"ready"}}}` + "\n"
			inPath, outPath := filepath.Join(t.TempDir(), "in"), filepath.Join(t.TempDir(), "out")
			if err := os.WriteFile(inPath, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			in, err := os.Open(inPath)
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			out, err := os.Create(outPath)
			if err != nil {
				t.Fatal(err)
			}
			oldIn, oldOut := os.Stdin, os.Stdout
			os.Stdin, os.Stdout = in, out
			err = run(context.Background(), append(base, "mcp"))
			os.Stdin, os.Stdout = oldIn, oldOut
			if err != nil {
				t.Fatal(err)
			}
			if _, err := out.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			d := json.NewDecoder(out)
			var result map[string]any
			if err := d.Decode(&result); err != nil {
				t.Fatal(err)
			}
			if err := d.Decode(&result); err != nil {
				t.Fatal(err)
			}
			message := result["result"].(map[string]any)["structuredContent"].(map[string]any)["result"].(map[string]any)
			if instance := message["agent_instance"].(map[string]any); instance["id"] != "worker" || instance["label"] != "Worker" {
				t.Fatalf("MCP startup defaults: %v", result)
			}
		})
	}
}

func TestInvalidInstanceEnvironmentFailsBeforeOpeningBackend(t *testing.T) {
	for _, tc := range []struct{ id, label string }{{"bad id", ""}, {"", "orphan"}, {"ok", "bad\nlabel"}} {
		for _, cmd := range []string{"send", "replay", "wait", "cursor", "ack", "watch", "instance-resume", "mcp", "mcp-http", "task-claim", "task-list"} {
			t.Setenv("COMITIA_AGENT_INSTANCE", tc.id)
			t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", tc.label)
			db := filepath.Join(t.TempDir(), "must-not-exist.db")
			args := []string{"--db", db, cmd}
			if cmd == "send" {
				args = append(args, "--agent-instance", "valid-override")
			}
			if err := commandErr(t, args...); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("startup %s: %v", cmd, err)
			}
			if _, err := os.Stat(db); !os.IsNotExist(err) {
				t.Fatalf("invalid startup touched backend: %v", err)
			}
		}
	}
}

func TestAdministrativeCommandsIgnoreInvalidInstanceEnvironment(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			base, _ := instanceCommandSetup(t, remote)
			t.Setenv("COMITIA_AGENT_INSTANCE", "bad id")
			t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "bad\nlabel")
			w := command(t, append(base, "workspace-create", "--name", "admin")...)
			c := command(t, append(base, "context-create", "--workspace-id", w["ID"].(string), "--name", "admin")...)
			if c["Name"] != "admin" {
				t.Fatalf("admin command: %v", c)
			}
			commandAny(t, append(base, "context-list")...)
		})
	}
	t.Setenv("COMITIA_AGENT_INSTANCE", "bad id")
	t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "bad\nlabel")
	db := filepath.Join(t.TempDir(), "admin.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	if boot["credential"] == nil {
		t.Fatalf("bootstrap: %v", boot)
	}
	// An invalid listen address reaches the serve path without starting a daemon.
	if err := commandErr(t, "--db", db, "serve", "--listen", "invalid-address"); err == nil || errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("serve rejected inherited instance configuration: %v", err)
	}
}

func TestWatchInstanceDefault(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			base, cid := instanceCommandSetup(t, remote)
			command(t, append(base, "send", "--context-id", cid, "--dedupe-key", "m", "--body", "ready")...)
			t.Setenv("COMITIA_AGENT_INSTANCE", "worker")
			watch := func(extra ...string) {
				t.Helper()
				old := os.Stdout
				devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { os.Stdout = old; _ = devNull.Close() }()
				os.Stdout = devNull
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := run(ctx, append(append(base, "watch", "--context-id", cid, "--timeout-seconds", "0.1"), extra...)); err != nil {
					t.Fatal(err)
				}
			}
			cursor := append(base, "cursor", "--context-id", cid)
			watch()
			if got := command(t, cursor...); got["sequence"] != float64(1) {
				t.Fatalf("default watch cursor: %v", got)
			}
			if got := command(t, append(cursor, "--agent-instance", "")...); got["sequence"] != float64(0) {
				t.Fatalf("default watch moved legacy cursor: %v", got)
			}
			watch("--agent-instance", "")
			if got := command(t, append(cursor, "--agent-instance", "")...); got["sequence"] != float64(1) {
				t.Fatalf("explicit legacy watch cursor: %v", got)
			}
		})
	}
}

func TestPresenceCommandsUseInstanceDefaults(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			base, _ := instanceCommandSetup(t, remote)
			w := command(t, append(base, "workspace-create", "--name", "presence")...)
			resume := filepath.Join(t.TempDir(), "resume.json")
			if err := os.WriteFile(resume, []byte(`{"pid":1}`), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("COMITIA_AGENT_INSTANCE", "worker")
			t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "Worker")
			for _, args := range [][]string{
				{"replay"},
				{"wait", "--timeout-seconds", "0"},
				{"watch", "--timeout-seconds", "0.1", "--no-ack"},
				{"instance-resume", "--resume-file", resume},
			} {
				for _, explicitEmpty := range []bool{false, true} {
					c := command(t, append(base, "context-create", "--workspace-id", w["ID"].(string), "--name", args[0])...)
					cid := c["ID"].(string)
					call := append(append(append([]string{}, base...), args[0], "--context-id", cid), args[1:]...)
					if explicitEmpty {
						call = append(call, "--agent-instance", "")
					}
					attempt := func() ([]any, error) {
						old := os.Stdout
						devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
						if err != nil {
							t.Fatal(err)
						}
						os.Stdout = devNull
						// watch runs until its deadline; the others finish on their own.
						deadline := 10 * time.Second
						if args[0] == "watch" {
							deadline = 3 * time.Second
						}
						ctx, cancel := context.WithTimeout(context.Background(), deadline)
						err = run(ctx, call)
						cancel()
						os.Stdout = old
						_ = devNull.Close()
						return commandAny(t, append(base, "instances", "--context-id", cid)...).([]any), err
					}
					rows, err := attempt()
					// A read heartbeat is best effort: under load it is skipped once
					// its small time budget runs out, so read again before judging.
					// Bound the retries by time, not by count, which a slow machine exhausts.
					for retry := time.Now().Add(30 * time.Second); !explicitEmpty && err == nil && len(rows) == 0 && time.Now().Before(retry); {
						rows, err = attempt()
					}
					if explicitEmpty {
						// Resume always names an instance; reads with an empty ID record none.
						if args[0] == "instance-resume" {
							if !errors.Is(err, comitia.ErrInvalid) {
								t.Fatalf("resume without instance: %v", err)
							}
						} else if err != nil || len(rows) != 0 {
							t.Fatalf("%s explicit empty instance: %v %v", args[0], rows, err)
						}
						continue
					}
					if err != nil || len(rows) != 1 || rows[0].(map[string]any)["id"] != "worker" {
						t.Fatalf("%s default presence: %v %v", args[0], rows, err)
					}
				}
			}
		})
	}
}
