package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/sqlite"
)

func TestWatchCommandServerRestart(t *testing.T) {
	if os.Getenv("COMITIA_WATCH_TEST_HELPER") == "1" {
		var args []string
		if json.Unmarshal([]byte(os.Getenv("COMITIA_WATCH_TEST_ARGS")), &args) != nil {
			os.Exit(11)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		go func() {
			var b [1]byte
			_, _ = os.Stdin.Read(b[:])
			cancel()
		}()
		if err := run(ctx, args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(12)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "watch.db")
	store, err := sqlite.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "test", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w, err := svc.CreateWorkspace(ctx, owner.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: w.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := svc.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	appendMessage := func(key, recipient, body string) comitia.Message {
		t.Helper()
		m, err := svc.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: key, Body: body, AgentInstance: &comitia.AgentInstance{ID: "writer"}, Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{recipient}, TaskID: task.ID, Payload: json.RawMessage(`{}`)}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	old := appendMessage("old", "reader", "already consumed")
	if err := svc.AckMessages(ctx, owner.Token, c.ID, old.Sequence, "reader"); err != nil {
		t.Fatal(err)
	}
	appendMessage("skip", "other", "not for reader")
	first := appendMessage("first", "reader", "first line\nsecond line")
	startServer := func(addr string) (*http.Server, string) {
		t.Helper()
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{Handler: httpapi.NewHandler(svc)}
		go func() { _ = server.Serve(listener) }()
		return server, listener.Addr().String()
	}
	server, addr := startServer("127.0.0.1:0")
	defer func() { _ = server.Close() }()
	args, _ := json.Marshal([]string{"--server-url", "http://" + addr, "--credential", owner.Token, "watch", "--context-id", c.ID, "--agent-instance", "reader", "--for-instance", "reader", "--from-instance", "writer", "--task-id", task.ID, "--schema", "comitia.discussion.v1", "--timeout-seconds", "0.3"})
	childCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(childCtx, os.Args[0], "-test.run=^TestWatchCommandServerRestart$")
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "COMITIA_") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "COMITIA_WATCH_TEST_HELPER=1", "COMITIA_WATCH_TEST_ARGS="+string(args))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdin.Close()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	waited := false
	defer func() {
		cancel()
		if !waited {
			_ = cmd.Process.Kill()
			<-done
		}
	}()
	lines := make(chan string, 10)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	readLine := func(want string) {
		t.Helper()
		select {
		case line, ok := <-lines:
			if !ok || line != want {
				t.Fatalf("watch line %q, want %q", line, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("watch stalled")
		}
	}
	waitAck := func(sequence int64) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			cursor, err := svc.MessageCursor(ctx, owner.Token, c.ID, "reader")
			if err != nil {
				t.Fatal(err)
			}
			if cursor == sequence {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("cursor stalled at %d, want %d", cursor, sequence)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	readLine(fmt.Sprintf("%d\twriter\tcomitia.discussion.v1\tfirst line", first.Sequence))
	waitAck(first.Sequence)
	// Stop the listener and all active waits, then reopen the same database and
	// listen on the same port. The watch process stays alive throughout.
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = sqlite.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	svc, err = comitia.New(comitia.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	second := appendMessage("second", "reader", "after restart")
	time.Sleep(350 * time.Millisecond)
	server, _ = startServer(addr)
	readLine(fmt.Sprintf("%d\twriter\tcomitia.discussion.v1\tafter restart", second.Sequence))
	waitAck(second.Sequence)
	for _, instance := range []string{"", "unrelated"} {
		cursor, err := svc.MessageCursor(ctx, owner.Token, c.ID, instance)
		if err != nil || cursor != 0 {
			t.Fatalf("watch moved other cursor %q: %d %v", instance, cursor, err)
		}
	}
	if _, err := stdin.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		waited = true
		if err != nil {
			t.Fatalf("watch exit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch ignored cancellation")
	}
	for line := range lines {
		t.Fatalf("unexpected duplicate watch line: %q", line)
	}
}

type watchAckFailureAPI struct {
	comitia.MessageAPI
	ackFailures int
	onAck       func(int64)
}

func (a *watchAckFailureAPI) AckMessages(ctx context.Context, token, cid string, seq int64, instances ...string) error {
	if a.ackFailures > 0 {
		a.ackFailures--
		return errors.New("temporary connection failure")
	}
	err := a.MessageAPI.AckMessages(ctx, token, cid, seq, instances...)
	if err == nil && a.onAck != nil {
		a.onAck(seq)
	}
	return err
}

type cancelWatchWriter struct {
	io.Writer
	cancel context.CancelFunc
}

func (w cancelWatchWriter) Write(b []byte) (int, error) {
	n, err := w.Writer.Write(b)
	w.cancel()
	return n, err
}

func watchFixture(t *testing.T) (comitia.API, string, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "messages.db")
	api, close, err := open(config{db: path, bootstrapCredential: "local"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(close)
	ctx := context.Background()
	_, owner, err := api.Bootstrap(ctx, comitia.BootstrapInput{Credential: "local", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := api.CreateWorkspace(ctx, owner.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	c, err := api.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: workspace.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	return api, owner.Token, c.ID, path
}

func TestWatchNoAckAndAckRetry(t *testing.T) {
	for _, ack := range []bool{false, true} {
		t.Run(fmt.Sprint("ack=", ack), func(t *testing.T) {
			api, token, cid, _ := watchFixture(t)
			for i := 1; i <= 2; i++ {
				if _, err := api.AppendMessage(context.Background(), token, cid, comitia.MessageInput{DedupeKey: fmt.Sprint(i), Body: fmt.Sprintf("line %d\nignored", i)}); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			probe := &watchAckFailureAPI{MessageAPI: api, ackFailures: 1, onAck: func(seq int64) {
				if seq == 2 {
					cancel()
				}
			}}
			var output bytes.Buffer
			var writer io.Writer = &output
			if !ack {
				writer = cancelWatchWriter{&output, cancel}
			}
			if err := watchMessages(ctx, probe, token, cid, "reader", time.Second, comitia.MessageFilters{}, ack, writer); err != nil {
				t.Fatal(err)
			}
			// Even if an acknowledgement fails after print, reconnect resumes
			// after that print and does not duplicate it or lose the next item.
			want := "1\t-\t-\tline 1\n2\t-\t-\tline 2\n"
			if output.String() != want {
				t.Fatalf("output %q, want %q", output.String(), want)
			}
			cursor, err := api.MessageCursor(context.Background(), token, cid, "reader")
			wantCursor := int64(0)
			if ack {
				wantCursor = 2
			}
			if err != nil || cursor != wantCursor {
				t.Fatalf("cursor %d, want %d: %v", cursor, wantCursor, err)
			}
		})
	}
	if err := watchMessages(context.Background(), nil, "", "", "", 0, comitia.MessageFilters{}, true, io.Discard); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("watch zero timeout: %v", err)
	}
}

func TestReplayAndWaitCommandFilters(t *testing.T) {
	api, token, cid, path := watchFixture(t)
	ctx := context.Background()
	task, err := api.CreateTask(ctx, token, cid, comitia.TaskInput{Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := api.AppendMessage(ctx, token, cid, comitia.MessageInput{DedupeKey: "typed", Body: "ready", AgentInstance: &comitia.AgentInstance{ID: "writer"}, Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{"reader"}, TaskID: task.ID, Payload: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"replay", "wait"} {
		args := []string{"--db", path, "--credential", token, cmd, "--context-id", cid, "--for-instance", "reader", "--from-instance", "writer", "--task-id", task.ID, "--schema", "comitia.discussion.v1", "--timeout-seconds", "0"}
		got := commandAny(t, args...).([]any)
		if len(got) != 1 || got[0].(map[string]any)["ID"] != m.ID {
			t.Fatalf("%s: %v", cmd, got)
		}
		for _, flag := range []string{"--for-instance", "--from-instance", "--task-id", "--schema"} {
			otherArgs := append([]string{}, args...)
			for i := range otherArgs {
				if otherArgs[i] == flag {
					otherArgs[i+1] = "other"
					break
				}
			}
			got := commandAny(t, otherArgs...)
			if got != nil && len(got.([]any)) != 0 {
				t.Fatalf("%s ignored %s: %v", cmd, flag, got)
			}
		}
	}
}

func TestWatchInstanceDoesNotSkipSessionAddressedMessage(t *testing.T) {
	api, token, cid, _ := watchFixture(t)
	ctx := context.Background()
	participants, err := api.ListParticipants(ctx, token, cid, 10)
	if err != nil || len(participants) != 1 {
		t.Fatalf("participants: %v %v", participants, err)
	}
	inputs := []comitia.MessageInput{
		{DedupeKey: "session", Body: "to your session", Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientSessionIDs: []string{participants[0].SessionID}, Payload: json.RawMessage(`{}`)}},
		{DedupeKey: "broadcast", Body: "later broadcast"},
	}
	for _, in := range inputs {
		if _, err := api.AppendMessage(ctx, token, cid, in); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	probe := &watchAckFailureAPI{MessageAPI: api, onAck: func(sequence int64) {
		if sequence == 2 {
			cancel()
		}
	}}
	var output bytes.Buffer
	if err := watchMessages(ctx, probe, token, cid, "reader", time.Second, comitia.MessageFilters{ForInstance: "reader"}, true, &output); err != nil {
		t.Fatal(err)
	}
	want := "1\t-\tcomitia.discussion.v1\tto your session\n2\t-\t-\tlater broadcast\n"
	if output.String() != want {
		t.Fatalf("watch skipped session message: %q, want %q", output.String(), want)
	}
	cursor, err := api.MessageCursor(context.Background(), token, cid, "reader")
	if err != nil || cursor != 2 {
		t.Fatalf("cursor: %d %v", cursor, err)
	}
}

func TestEscapeWatchField(t *testing.T) {
	for in, want := range map[string]string{
		"plain text: ok, 'quoted' \"x\"": "plain text: ok, 'quoted' \"x\"",
		"héllo 世界 \u00a0":                "héllo 世界 \u00a0",
		"a\tb\rc\nd":                     `a\tb\rc\nd`,
		"\x1b[2K\x1b]52;c;eA==\a":        `\x1b[2K\x1b]52;c;eA==\a`,
		"\x00\x7f":                       `\x00\x7f`,
		"\u0085\u009b31m":                `\u0085\u009b31m`,
		"\u202eevil\u2028":               `\u202eevil\u2028`,
		"bad\xff\xc3utf8":                `bad\xff\xc3utf8`,
		`C:\path\x1b`:                    `C:\\path\\x1b`,
	} {
		if got := escapeWatchField(in); got != want {
			t.Errorf("escapeWatchField(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWatchEscapesTerminalControls(t *testing.T) {
	api, token, cid, _ := watchFixture(t)
	body := "safe\r\x1b[2Kforged approval\x1b]52;c;Y2xpcGJvYXJk\a\ttab\u009b\xff\r\nsecond line"
	if _, err := api.AppendMessage(context.Background(), token, cid, comitia.MessageInput{DedupeKey: "controls", Body: body}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	if err := watchMessages(ctx, api, token, cid, "reader", time.Second, comitia.MessageFilters{}, false, cancelWatchWriter{&out, cancel}); err != nil {
		t.Fatal(err)
	}
	want := "1\t-\t-\t" + `safe\r\x1b[2Kforged approval\x1b]52;c;Y2xpcGJvYXJk\a\ttab\u009b\xff` + "\n"
	if out.String() != want {
		t.Fatalf("output %q, want %q", out.String(), want)
	}
}

func TestWatchJSONLinesProcessThenAck(t *testing.T) {
	api, token, cid, _ := watchFixture(t)
	ctx := context.Background()
	root, err := api.AppendMessage(ctx, token, cid, comitia.MessageInput{DedupeKey: "root", Body: "root"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := api.AppendMessage(ctx, token, cid, comitia.MessageInput{DedupeKey: "full", Body: "first\nsecond\t\x1b\u202e", AgentInstance: &comitia.AgentInstance{ID: "writer", Label: "Worker"}, Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", ReplyTo: root.ID, RecipientInstances: []string{"reader"}, Payload: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if err := api.AckMessages(ctx, token, cid, root.Sequence, "consumer"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		runCtx, cancel := context.WithTimeout(ctx, time.Second)
		var output bytes.Buffer
		err := watchMessageOutput(runCtx, api, token, cid, "consumer", time.Second, comitia.MessageFilters{}, false, true, cancelWatchWriter{&output, cancel})
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Count(output.Bytes(), []byte("\n")) != 1 {
			t.Fatalf("multiline frame: %q", output.String())
		}
		var decoded comitia.Message
		if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &decoded); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(decoded, message) {
			t.Fatalf("full record lost: %+v want %+v", decoded, message)
		}
		cursor, err := api.MessageCursor(ctx, token, cid, "consumer")
		if err != nil || cursor != root.Sequence {
			t.Fatalf("print acknowledged business effect: %d %v", cursor, err)
		}
	}
	// Business state was durably committed before the explicit ack. Restart will
	// now skip exactly that message; the separate terminal cursor was untouched.
	if err := api.AckMessages(ctx, token, cid, message.Sequence, "consumer"); err != nil {
		t.Fatal(err)
	}
	remaining, err := api.ReplayMessages(ctx, token, cid, message.Sequence, 100)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("restart: %v %v", remaining, err)
	}
	if cursor, err := api.MessageCursor(ctx, token, cid, "terminal"); err != nil || cursor != 0 {
		t.Fatalf("other consumer moved: %d %v", cursor, err)
	}
}

type failedWatchWriter struct{}

func (failedWatchWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestWatchJSONLinesOutputFailureLeavesCursor(t *testing.T) {
	api, token, cid, _ := watchFixture(t)
	if _, err := api.AppendMessage(context.Background(), token, cid, comitia.MessageInput{DedupeKey: "failure", Body: "unhandled"}); err != nil {
		t.Fatal(err)
	}
	if err := watchMessageOutput(context.Background(), api, token, cid, "consumer", time.Second, comitia.MessageFilters{}, false, true, failedWatchWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("output error: %v", err)
	}
	if cursor, err := api.MessageCursor(context.Background(), token, cid, "consumer"); err != nil || cursor != 0 {
		t.Fatalf("failed output acknowledged: %d %v", cursor, err)
	}
}

func TestWatchJSONLinesRequiresManualAck(t *testing.T) {
	api, token, cid, _ := watchFixture(t)
	cfg := config{credential: token}
	// Runtime flag errors precede the long-lived loop.
	if err := runMessages(context.Background(), cfg, api, json.NewEncoder(io.Discard), "watch", []string{"--context-id", cid, "--json-lines"}); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("implicit auto ack: %v", err)
	}
	for _, args := range [][]string{{"--json-lines", "--manual-ack"}, {"--json-lines", "--no-ack"}} {
		cfg.parseOnly = true
		if err := runMessages(context.Background(), cfg, nil, nil, "watch", args); err != nil {
			t.Fatal(err)
		}
	}
}
