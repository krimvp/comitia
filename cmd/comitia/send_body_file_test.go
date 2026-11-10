package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krimvp/comitia"
)

func withStdin(t *testing.T, data string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdin
	os.Stdin = f
	t.Cleanup(func() { os.Stdin = old; _ = f.Close() })
}

// commandViaFile captures stdout in a file, since a pipe would block on a large record.
func commandViaFile(t *testing.T, args ...string) map[string]any {
	t.Helper()
	f, err := os.Create(filepath.Join(t.TempDir(), "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	old := os.Stdout
	os.Stdout = f
	err = run(context.Background(), args)
	os.Stdout = old
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	b, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// Bodies past the OS argument limit arrive through a file or stdin.
func TestSendReadsBodyAndEnvelopeFromFileOrStdin(t *testing.T) {
	db := filepath.Join(t.TempDir(), "body.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	base := []string{"--db", db, "--credential", boot["credential"].(map[string]any)["Token"].(string)}
	w := command(t, append(base, "workspace-create", "--name", "w")...)
	c := command(t, append(base, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")...)
	send := append(base, "send", "--context-id", c["ID"].(string))

	large := strings.Repeat("a", 900000) + "\n"
	withStdin(t, large)
	if m := commandViaFile(t, append(send, "--dedupe-key", "stdin", "--body-file", "-")...); m["Body"] != large {
		t.Fatalf("stdin body has %d bytes", len(m["Body"].(string)))
	}
	path := filepath.Join(t.TempDir(), "body.txt")
	if err := os.WriteFile(path, []byte("from file"), 0600); err != nil {
		t.Fatal(err)
	}
	if m := command(t, append(send, "--dedupe-key", "file", "--body-file", path)...); m["Body"] != "from file" {
		t.Fatalf("file body=%v", m["Body"])
	}
	withStdin(t, `{"schema":"comitia.discussion.v1","payload":{}}`)
	if m := command(t, append(send, "--dedupe-key", "envelope", "--body", "hi", "--envelope-file", "-")...); m["envelope"] == nil {
		t.Fatalf("stdin envelope missing: %v", m)
	}
	withStdin(t, strings.Repeat("a", comitia.MaxMessageBodyBytes+1))
	if err := commandErr(t, append(send, "--dedupe-key", "big", "--body-file", "-")...); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("oversized body: %v", err)
	}
	for want, args := range map[string][]string{
		"--body and --body-file are mutually exclusive":              {"--body", "x", "--body-file", path},
		"only one of --body-file and --envelope-file can read stdin": {"--body-file", "-", "--envelope-file", "-"},
	} {
		err := commandErr(t, append(append(send, "--dedupe-key", "k"), args...)...)
		if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%v: err=%v", args, err)
		}
	}
}
