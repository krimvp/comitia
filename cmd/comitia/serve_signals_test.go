package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/httpapi"
	"github.com/krimvp/comitia/pkg/sqlite"
)

func TestServeSignalProcess(t *testing.T) {
	if os.Getenv("COMITIA_TEST_SIGNAL_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"comitia"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(2)
}

// startSignalProcess runs the CLI in a child process with a clean COMITIA_ environment.
func startSignalProcess(t *testing.T, stdin io.Reader, stdout io.Writer, args ...string) (*exec.Cmd, *bytes.Buffer, <-chan error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], append([]string{"-test.run=^TestServeSignalProcess$", "--"}, args...)...)
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "COMITIA_") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "COMITIA_TEST_SIGNAL_PROCESS=1", "HOME="+t.TempDir())
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done, exited := make(chan error, 1), make(chan struct{})
	go func() { done <- cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		<-exited
	})
	return cmd, &stderr, done
}

func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func waitListening(t *testing.T, addr string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if conn, err := net.Dial("tcp", addr); err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("listener did not start")
		}
	}
}

func terminateAndWait(t *testing.T, cmd *exec.Cmd, stderr *bytes.Buffer, done <-chan error) {
	t.Helper()
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitStopped(t, stderr, done)
}

func waitStopped(t *testing.T, stderr *bytes.Buffer, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exit: %v: %s", err, stderr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("process did not stop")
	}
	if !strings.Contains(stderr.String(), "stopping signal=terminated") {
		t.Errorf("stop not logged: %s", stderr)
	}
}

// signalFixture creates an embedded database with an owner and one context.
func signalFixture(t *testing.T) (db, token, contextID string) {
	t.Helper()
	db = filepath.Join(t.TempDir(), "signal.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	token = boot["credential"].(map[string]any)["Token"].(string)
	w := command(t, "--db", db, "--credential", token, "workspace-create", "--name", "w")
	c := command(t, "--db", db, "--credential", token, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")
	return db, token, c["ID"].(string)
}

func TestServeSIGTERMDrainsLongPollsAndClosesStore(t *testing.T) {
	db, token, cid := signalFixture(t)
	addr := freeAddress(t)
	cmd, stderr, done := startSignalProcess(t, nil, io.Discard, "--db", db, "serve", "--listen", addr)
	waitListening(t, addr)
	client, err := httpapi.NewClient("http://"+addr, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AppendMessage(context.Background(), token, cid, comitia.MessageInput{DedupeKey: "before", Body: "write"}); err != nil {
		t.Fatal(err)
	}
	type result struct {
		messages []comitia.Message
		err      error
	}
	waited := make(chan result, 1)
	go func() {
		messages, err := client.WaitMessages(context.Background(), token, cid, 1, 30*time.Second)
		waited <- result{messages, err}
	}()
	time.Sleep(300 * time.Millisecond)
	started := time.Now()
	terminateAndWait(t, cmd, stderr, done)
	select {
	case r := <-waited:
		if r.err != nil || len(r.messages) != 0 {
			t.Fatalf("drained wait: %v %v", r.messages, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long poll was not drained")
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("shutdown took %v", elapsed)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(db + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("store not closed: %s %v", suffix, err)
		}
	}
}

func TestMCPHTTPSIGTERMDrainsLongPolls(t *testing.T) {
	store, err := sqlite.OpenSQLite(filepath.Join(t.TempDir(), "daemon.db"))
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
	daemon := httptest.NewServer(httpapi.NewHandler(svc))
	defer daemon.Close()
	addr := freeAddress(t)
	cmd, stderr, done := startSignalProcess(t, nil, io.Discard, "--server-url", daemon.URL, "mcp-http", "--listen", addr)
	waitListening(t, addr)
	type result struct {
		body []byte
		err  error
	}
	waited := make(chan result, 1)
	go func() {
		call := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":"` + c.ID + `","timeout_seconds":30}}}`
		req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/mcp", strings.NewReader(call))
		req.Header.Set("Authorization", "Bearer "+owner.Token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			waited <- result{nil, err}
			return
		}
		defer res.Body.Close()
		body, err := io.ReadAll(res.Body)
		waited <- result{body, err}
	}()
	time.Sleep(300 * time.Millisecond)
	terminateAndWait(t, cmd, stderr, done)
	select {
	case r := <-waited:
		if r.err != nil || !bytes.Contains(r.body, []byte(`"structuredContent":{"result":[]}`)) || bytes.Contains(r.body, []byte(`"isError":true`)) {
			t.Fatalf("drained wait: %s %v", r.body, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("long poll was not drained")
	}
}

func TestMCPStdioSIGTERMAnswersLongPollsAndClosesStore(t *testing.T) {
	db, token, cid := signalFixture(t)
	stdin, input := io.Pipe()
	defer input.Close()
	output, stdout := io.Pipe()
	cmd, stderr, done := startSignalProcess(t, stdin, stdout, "--db", db, "--credential", token, "mcp")
	lines := make(chan string, 4)
	go func() {
		scan := bufio.NewScanner(output)
		for scan.Scan() {
			lines <- scan.Text()
		}
		close(lines)
	}()
	readLine := func() string {
		t.Helper()
		select {
		case line := <-lines:
			return line
		case <-time.After(10 * time.Second):
			t.Fatal("no response")
			return ""
		}
	}
	if _, err := io.WriteString(input, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	readLine()
	if _, err := io.WriteString(input, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":"`+cid+`","timeout_seconds":30}}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	var reply struct {
		ID     int `json:"id"`
		Result struct {
			IsError           bool `json:"isError"`
			StructuredContent struct {
				Result []any `json:"result"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if line := readLine(); json.Unmarshal([]byte(line), &reply) != nil || reply.ID != 2 || reply.Result.IsError || reply.Result.StructuredContent.Result == nil {
		t.Fatalf("drained wait: %s", line)
	}
	// The reply arrived with input still open; release exec's stdin copy so Wait returns.
	_ = input.Close()
	waitStopped(t, stderr, done)
	_ = stdout.Close()
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(db + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("store not closed: %s %v", suffix, err)
		}
	}
}
