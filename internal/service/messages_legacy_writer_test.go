package service

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/sqlstore"

	"github.com/krimvp/comitia/internal/model"
)

// Preserve the pre-projection SQLite writer from 2df3561 in a public CLI
// harness. Building from source also works in shallow/source-only checkouts.
// The override runs this same regression against an exact historical binary.
func legacyMessageCLI(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("COMITIA_TEST_LEGACY_CLI"); binary != "" {
		return binary
	}
	dir := t.TempDir()
	// The tree is copied from the module root, two levels up.
	root := filepath.Join("..", "..")
	for _, pattern := range []string{"*.go", "go.mod", "go.sum", "internal/*/*.go", "cmd/comitia/*.go", "httpapi/*.go", "httpapi/ui/*", "mcp/*.go", "sqlite/*.go"} {
		paths, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			// Test access reaches helpers of the current messages file that
			// the historical one does not have.
			if info.IsDir() || strings.HasSuffix(path, "_test.go") || rel == filepath.Join("internal", "sqlstore", "testaccess.go") {
				continue
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(dest), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dest, raw, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	raw, err := os.ReadFile("testdata/legacy_sqlite_messages.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "sqlstore", "messages.go"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "comitia")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/comitia")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=go1.24.13", "GOFLAGS=-buildvcs=false")
	if raw, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build historical writer: %s %v", raw, err)
	}
	return binary
}

func TestRunningReaderSeesOlderCLIWriter(t *testing.T) {
	binary := legacyMessageCLI(t)
	path := filepath.Join(t.TempDir(), "messages.db")
	f := setup(t, path)
	defer f.store.Close()
	ctx := context.Background()
	// Isolate real CLI wall-clock credentials to this temporary database.
	*f.now = time.Now().UTC()
	for _, table := range []string{"sessions", "grants"} {
		if _, err := sqlstore.DB(f.store).Exec("UPDATE "+table+" SET expires_at=?", model.TS(f.now.Add(time.Hour))); err != nil {
			t.Fatal(err)
		}
	}
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	appendLegacy := func(key string, envelope *model.MessageEnvelope, sender string) model.Message {
		t.Helper()
		args := []string{"--db", path, "send", "--context-id", f.context.ID, "--dedupe-key", key, "--body", key}
		if sender != "" {
			args = append(args, "--agent-instance", sender)
		}
		if envelope != nil {
			raw, err := json.Marshal(envelope)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(home, key+".json")
			if err := os.WriteFile(file, raw, 0600); err != nil {
				t.Fatal(err)
			}
			args = append(args, "--envelope-file", file)
		}
		cmd := exec.CommandContext(ctx, binary, args...)
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "COMITIA_") && !strings.HasPrefix(kv, "HOME=") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		cmd.Env = append(cmd.Env, "HOME="+home, "COMITIA_CREDENTIAL="+f.admin.Token)
		raw, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("older CLI send: %s %v", raw, err)
		}
		var m model.Message
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	filter := model.MessageFilters{ForInstance: "reader"}
	waiting := make(chan waitResult, 1)
	waitCtx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		messages, err := f.svc.WaitMessages(waitCtx, f.admin.Token, f.context.ID, 1, 30*time.Second, filter)
		waiting <- waitResult{messages, err}
	}()
	m := appendLegacy("older-broadcast", nil, "")
	all, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 1, 100)
	if err != nil || len(all) != 1 {
		t.Fatalf("unfiltered: %v %v", all, err)
	}
	page, err := f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, 1, 100, filter)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != m.ID || page.ScannedThrough != m.Sequence {
		t.Fatalf("older broadcast lost below watermark: %+v %v", page, err)
	}
	got := receiveWait(t, waiting)
	if got.err != nil || len(got.messages) != 1 || got.messages[0].ID != m.ID {
		t.Fatalf("waiting reader missed older append: %+v", got)
	}
	after := page.ScannedThrough
	for _, tc := range []struct {
		name                string
		instances, sessions []string
		eligible            bool
	}{
		{"typed-broadcast", nil, nil, true},
		{"session", nil, []string{f.admin.Session.ID}, true},
		{"other-instance", []string{"other"}, []string{f.admin.Session.ID}, false},
		{"reader-instance", []string{"reader", "other"}, []string{f.admin.Session.ID}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			envelope := &model.MessageEnvelope{Schema: "comitia.discussion.v1", TaskID: task.ID, RecipientInstances: tc.instances, RecipientSessionIDs: tc.sessions, Payload: json.RawMessage(`{}`)}
			m := appendLegacy(tc.name, envelope, "worker")
			for _, filter := range []model.MessageFilters{{ForInstance: "reader"}, {Schema: envelope.Schema}, {TaskID: task.ID}, {FromInstance: "worker"}, {ForInstance: "reader", Schema: envelope.Schema, TaskID: task.ID, FromInstance: "worker"}} {
				want := 1
				if filter.ForInstance != "" && !tc.eligible {
					want = 0
				}
				for _, wait := range []bool{false, true} {
					var page model.MessagePage
					var err error
					if wait {
						page, err = f.svc.WaitMessagesPage(ctx, f.admin.Token, f.context.ID, after, 0, filter)
					} else {
						page, err = f.svc.ReplayMessagesPage(ctx, f.admin.Token, f.context.ID, after, 100, filter)
					}
					if err != nil || len(page.Messages) != want || page.ScannedThrough != m.Sequence {
						t.Fatalf("wait=%v filter=%+v: %+v %v", wait, filter, page, err)
					}
					if want != 0 && page.Messages[0].ID != m.ID {
						t.Fatalf("wrong match: %+v", page)
					}
				}
			}
			after = m.Sequence
		})
	}
	for _, instance := range []string{"", "reader"} {
		if cursor, err := f.svc.MessageCursor(ctx, f.admin.Token, f.context.ID, instance); err != nil || cursor != 0 {
			t.Fatalf("instance %q scan ack: %d %v", instance, cursor, err)
		}
	}
	t.Logf("older CLI appends remained visible to open replay/wait readers through sequence %d", after)
}
