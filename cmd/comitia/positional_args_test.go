package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

// usageCommands lists every command named in the global usage table.
func usageCommands(t *testing.T) []string {
	t.Helper()
	var commands []string
	for _, line := range strings.Split(commandUsage, "\n") {
		if !strings.HasPrefix(line, "  ") {
			continue
		}
		names, _, _ := strings.Cut(strings.TrimSpace(line), "  ")
		for _, name := range strings.Split(names, ",") {
			commands = append(commands, strings.TrimSpace(name))
		}
	}
	if len(commands) < 30 {
		t.Fatalf("usage table parsed to %v", commands)
	}
	return commands
}

func TestEveryCommandRejectsPositionalArguments(t *testing.T) {
	for _, cmd := range usageCommands(t) {
		for name, args := range map[string][]string{"bare": {"stray"}, "after-terminator": {"--", "stray"}} {
			t.Run(cmd+"/"+name, func(t *testing.T) {
				db := filepath.Join(t.TempDir(), "args.db")
				err := run(context.Background(), append([]string{"--db", db, "--server-url=", cmd}, args...))
				if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), `unexpected argument "stray"`) {
					t.Fatalf("err=%v", err)
				}
				if _, err := os.Stat(db); !os.IsNotExist(err) {
					t.Fatalf("rejected command touched storage: %v", err)
				}
			})
		}
	}
}

func TestUnquotedBodyIsRejectedNotTruncated(t *testing.T) {
	db := filepath.Join(t.TempDir(), "args.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	cred := boot["credential"].(map[string]any)["Token"].(string)
	base := []string{"--db", db, "--credential", cred}
	w := command(t, append(base, "workspace-create", "--name", "w")...)
	c := command(t, append(base, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")...)
	cid := c["ID"].(string)
	err := commandErr(t, append(base, "send", "--context-id", cid, "--dedupe-key", "k1", "--body", "hello", "world")...)
	if err == nil || !strings.Contains(err.Error(), `unexpected argument "world"`) {
		t.Fatalf("err=%v", err)
	}
	if got := commandAny(t, append(base, "replay", "--context-id", cid)...).([]any); len(got) != 0 {
		t.Fatalf("truncated message stored: %v", got)
	}
	m := command(t, append(base, "send", "--context-id", cid, "--dedupe-key", "k1", "--body", "hello world")...)
	if m["Body"] != "hello world" {
		t.Fatalf("message=%v", m)
	}
}
