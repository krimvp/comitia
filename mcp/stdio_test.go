package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

type stdioResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type stdioClient struct {
	in        *io.PipeWriter
	responses chan stdioResponse
	readErr   chan error
	done      chan error
	cancel    context.CancelFunc
}

func startStdio(t *testing.T, s *Server) *stdioClient {
	t.Helper()
	in, input := io.Pipe()
	output, out := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	c := &stdioClient{in: input, responses: make(chan stdioResponse, 64), readErr: make(chan error, 1), done: make(chan error, 1), cancel: cancel}
	go func() {
		c.done <- s.Serve(ctx, in, out)
		_ = out.Close()
	}()
	go func() {
		defer close(c.responses)
		scan := bufio.NewScanner(output)
		for scan.Scan() {
			var res stdioResponse
			if err := json.Unmarshal(scan.Bytes(), &res); err != nil {
				c.readErr <- err
				return
			}
			c.responses <- res
		}
		if err := scan.Err(); err != nil {
			c.readErr <- err
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = input.Close()
		_ = output.Close()
	})
	return c
}

func (c *stdioClient) send(t *testing.T, msg string) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, err := io.WriteString(c.in, msg+"\n")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("stdio stopped reading input")
	}
}

func (c *stdioClient) read(t *testing.T) stdioResponse {
	t.Helper()
	select {
	case res, ok := <-c.responses:
		if !ok {
			t.Fatal("stdio output closed")
		}
		return res
	case err := <-c.readErr:
		t.Fatal(err)
	case <-time.After(time.Second):
		t.Fatal("stdio response timed out")
	}
	return stdioResponse{}
}

func (c *stdioClient) initialize(t *testing.T) {
	t.Helper()
	c.send(t, `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if res := c.read(t); string(res.ID) != "0" || res.Error != nil {
		t.Fatalf("initialize: %+v", res)
	}
}

func (c *stdioClient) finish(t *testing.T) {
	t.Helper()
	_ = c.in.Close()
	select {
	case err := <-c.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stdio did not shut down")
	}
	for res := range c.responses {
		t.Errorf("unexpected response: %+v", res)
	}
}

type stdioProbe struct {
	comitia.API
	started, stopped chan string
	release          map[string]chan struct{}
	ignoreCancel     bool
}

func newStdioProbe() *stdioProbe {
	return &stdioProbe{started: make(chan string, 16), stopped: make(chan string, 16), release: make(map[string]chan struct{})}
}

func (p *stdioProbe) wait(ctx context.Context, id string) error {
	p.started <- id
	defer func() { p.stopped <- id }()
	if p.ignoreCancel {
		<-p.release[id]
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.release[id]:
		return nil
	}
}

func (p *stdioProbe) WaitMessages(ctx context.Context, _, cid string, _ int64, _ time.Duration, _ ...comitia.MessageFilters) ([]comitia.Message, error) {
	return []comitia.Message{}, p.wait(ctx, cid)
}

func (p *stdioProbe) GetTask(ctx context.Context, _, id string) (comitia.Task, error) {
	return comitia.Task{ID: id, Title: strings.Repeat(id, 4096)}, p.wait(ctx, id)
}

func probeEvent(t *testing.T, events <-chan string, want string) {
	t.Helper()
	select {
	case got := <-events:
		if got != want {
			t.Fatalf("event=%q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("no event for %q", want)
	}
}

func TestStdioPingAndListDuringWait(t *testing.T) {
	p := newStdioProbe()
	p.release["wait"] = make(chan struct{})
	c := startStdio(t, &Server{API: p, Credential: "configured"})
	c.initialize(t)
	c.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":"wait"}}}`)
	probeEvent(t, p.started, "wait")
	for _, method := range []string{"ping", "tools/list"} {
		c.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":2,"method":%q}`, method))
		if res := c.read(t); string(res.ID) != "2" || res.Error != nil {
			t.Fatalf("%s during wait: %+v", method, res)
		}
	}
	close(p.release["wait"])
	if res := c.read(t); string(res.ID) != "1" || res.Error != nil {
		t.Fatalf("wait: %+v", res)
	}
	c.finish(t)
}

func TestStdioCancelStopsWaitWithoutResponse(t *testing.T) {
	for _, id := range []string{"2", "9007199254740993", `"wait"`, `""`, `"escaped\u002dwait"`} {
		t.Run(id, func(t *testing.T) {
			p := newStdioProbe()
			c := startStdio(t, &Server{API: p, Credential: "configured"})
			c.initialize(t)
			c.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":"wait"}}}`, id))
			probeEvent(t, p.started, "wait")
			cancelID := []byte(id)
			if strings.HasPrefix(id, `"`) {
				var decoded string
				_ = json.Unmarshal([]byte(id), &decoded)
				cancelID, _ = json.Marshal(decoded)
			}
			c.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":%s,"reason":"test"}}`, cancelID))
			probeEvent(t, p.stopped, "wait")
			c.send(t, `{"jsonrpc":"2.0","id":3,"method":"ping"}`)
			if res := c.read(t); string(res.ID) != "3" || res.Error != nil {
				t.Fatalf("cancelled wait responded: %+v", res)
			}
			c.finish(t)
		})
	}
}

func TestStdioConcurrentCallIDs(t *testing.T) {
	p := newStdioProbe()
	for _, id := range []string{"first", "second"} {
		p.release[id] = make(chan struct{})
	}
	c := startStdio(t, &Server{API: p, Credential: "configured"})
	c.initialize(t)
	for _, id := range []string{"first", "second"} {
		c.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%q,"method":"tools/call","params":{"name":"get_task","arguments":{"task_id":%q}}}`, id, id))
		probeEvent(t, p.started, id)
	}
	for _, id := range []string{"second", "first"} {
		close(p.release[id])
		res := c.read(t)
		var result struct {
			StructuredContent struct {
				Result comitia.Task `json:"result"`
			} `json:"structuredContent"`
		}
		if err := json.Unmarshal(res.Result, &result); err != nil {
			t.Fatal(err)
		}
		if string(res.ID) != fmt.Sprintf("%q", id) || result.StructuredContent.Result.ID != id {
			t.Fatalf("response ID=%s task=%q, want %q", res.ID, result.StructuredContent.Result.ID, id)
		}
	}
	c.finish(t)
}

func TestStdioCapacityKeepsControlMessagesResponsive(t *testing.T) {
	p := newStdioProbe()
	c := startStdio(t, &Server{API: p, Credential: "configured"})
	c.initialize(t)
	for i := 1; i <= maxStdioCalls; i++ {
		id := fmt.Sprint(i)
		c.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":%q}}}`, i, id))
		probeEvent(t, p.started, id)
	}
	c.send(t, `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":"excess"}}}`)
	if res := c.read(t); string(res.ID) != "9" || res.Error == nil || res.Error.Code != -32000 {
		t.Fatalf("capacity: %+v", res)
	}
	c.send(t, `{"jsonrpc":"2.0","id":10,"method":"ping"}`)
	if res := c.read(t); string(res.ID) != "10" || res.Error != nil {
		t.Fatalf("ping at capacity: %+v", res)
	}
	for i := 1; i <= maxStdioCalls; i++ {
		c.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":%d}}`, i))
		probeEvent(t, p.stopped, fmt.Sprint(i))
	}
	c.finish(t)
}

func TestStdioIgnoresInvalidCancellation(t *testing.T) {
	for _, id := range []string{"1", `""`} {
		t.Run(id, func(t *testing.T) {
			p := newStdioProbe()
			p.release["wait"] = make(chan struct{})
			c := startStdio(t, &Server{API: p, Credential: "configured"})
			c.initialize(t)
			c.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":"wait"}}}`, id))
			probeEvent(t, p.started, "wait")
			for _, params := range []string{`{}`, `null`, `{"requestId":null}`, `{"requestId":"1"}`, `{"requestId":99}`, `{"requestId":{}}`, `{"requestId":[]}`, `{"requestId":1.5}`, fmt.Sprintf(`{"requestId":%s,"reason":42}`, id), `[]`} {
				c.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":`+params+`}`)
			}
			c.send(t, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
			if res := c.read(t); string(res.ID) != "2" || res.Error != nil {
				t.Fatalf("ping: %+v", res)
			}
			select {
			case <-p.stopped:
				t.Fatal("malformed or unknown cancellation stopped wait")
			default:
			}
			close(p.release["wait"])
			if res := c.read(t); string(res.ID) != id {
				t.Fatalf("wait: %+v", res)
			}
			c.finish(t)
		})
	}
}

func TestStdioShutdownCancelsCalls(t *testing.T) {
	for _, eof := range []bool{false, true} {
		t.Run(fmt.Sprintf("EOF=%v", eof), func(t *testing.T) {
			p := newStdioProbe()
			c := startStdio(t, &Server{API: p, Credential: "configured"})
			c.initialize(t)
			c.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":"wait"}}}`)
			probeEvent(t, p.started, "wait")
			if eof {
				_ = c.in.Close()
				select {
				case err := <-c.done:
					if err == nil || !strings.Contains(err.Error(), "EOF shutdown cancelled 1 tool calls") {
						t.Fatalf("EOF shutdown: %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("EOF did not stop Serve")
				}
				for res := range c.responses {
					t.Errorf("cancelled response: %+v", res)
				}
			} else {
				c.cancel()
				select {
				case err := <-c.done:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("shutdown: %v", err)
					}
				case <-time.After(time.Second):
					t.Fatal("context cancellation did not stop Serve")
				}
				for res := range c.responses {
					t.Errorf("cancelled response: %+v", res)
				}
			}
			probeEvent(t, p.stopped, "wait")
		})
	}
}

func TestStdioShutdownIsBoundedForUncooperativeCall(t *testing.T) {
	p := newStdioProbe()
	p.ignoreCancel = true
	p.release["wait"] = make(chan struct{})
	defer close(p.release["wait"])
	c := startStdio(t, &Server{API: p, Credential: "configured"})
	c.initialize(t)
	c.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":"wait"}}}`)
	probeEvent(t, p.started, "wait")
	_ = c.in.Close()
	select {
	case err := <-c.done:
		if err == nil || !strings.Contains(err.Error(), "shutdown deadline") {
			t.Fatalf("uncooperative shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown exceeded bound")
	}
}

// Yield between small writes so an unsynchronized encoder would corrupt lines
// and race on the buffer even when each response is encoded in one Write call.
type fragmentWriter struct{ bytes.Buffer }

func (w *fragmentWriter) Write(p []byte) (int, error) {
	length := len(p)
	for len(p) > 0 {
		n := min(len(p), 17)
		_, _ = w.Buffer.Write(p[:n])
		p = p[n:]
		runtime.Gosched()
	}
	return length, nil
}

func TestStdioOutputLinesNeverInterleave(t *testing.T) {
	p := newStdioProbe()
	var input strings.Builder
	input.WriteString(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n")
	for i := 1; i <= maxStdioCalls; i++ {
		id := fmt.Sprint(i)
		p.release[id] = make(chan struct{})
		fmt.Fprintf(&input, `{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"get_task","arguments":{"task_id":%q}}}`+"\n", i, id)
	}
	var output fragmentWriter
	done := make(chan error, 1)
	go func() {
		done <- (&Server{API: p, Credential: "configured"}).Serve(context.Background(), strings.NewReader(input.String()), &output)
	}()
	for i := 0; i < maxStdioCalls; i++ {
		select {
		case <-p.started:
		case <-time.After(time.Second):
			t.Fatal("concurrent call did not start")
		}
	}
	for _, release := range p.release {
		close(release)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	scan := bufio.NewScanner(&output)
	seen := make(map[string]bool)
	for scan.Scan() {
		var res stdioResponse
		if err := json.Unmarshal(scan.Bytes(), &res); err != nil || res.Error != nil {
			t.Fatalf("interleaved output: %v %+v", err, res)
		}
		if seen[string(res.ID)] {
			t.Fatalf("duplicate response: %s", res.ID)
		}
		seen[string(res.ID)] = true
	}
	if err := scan.Err(); err != nil || len(seen) != maxStdioCalls+1 {
		t.Fatalf("lines=%d: %v", len(seen), err)
	}
}

func TestStdioInitializeGate(t *testing.T) {
	c := startStdio(t, &Server{API: newStdioProbe(), Credential: "configured"})
	for _, method := range []string{"tools/call", "tools/list"} {
		c.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":%q}`, method))
		if res := c.read(t); res.Error == nil || res.Error.Code != -32002 {
			t.Fatalf("%s before initialize: %+v", method, res)
		}
	}
	c.send(t, `{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if res := c.read(t); res.Error != nil {
		t.Fatalf("ping before initialize: %+v", res)
	}
	c.send(t, `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{}}`)
	if res := c.read(t); res.Error == nil || res.Error.Code != -32602 {
		t.Fatalf("invalid initialize: %+v", res)
	}
	c.initialize(t)
	c.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":0}}`)
	c.send(t, `{"jsonrpc":"2.0","id":4,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if res := c.read(t); res.Error == nil || res.Error.Code != -32600 {
		t.Fatalf("repeat initialize: %+v", res)
	}
	c.finish(t)
}

func TestStdioRejectsDuplicateInFlightID(t *testing.T) {
	p := newStdioProbe()
	c := startStdio(t, &Server{API: p, Credential: "configured"})
	c.initialize(t)
	for _, id := range []string{`"wait"`, `"\u0077ait"`} {
		c.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":"wait"}}}`, id))
		if id == `"wait"` {
			probeEvent(t, p.started, "wait")
		} else if res := c.read(t); res.Error == nil || res.Error.Code != -32600 {
			t.Fatalf("duplicate request ID: %+v", res)
		}
	}
	c.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":"wait"}}`)
	probeEvent(t, p.stopped, "wait")
	c.finish(t)
}

func TestStdioRejectsInvalidToolCallIDs(t *testing.T) {
	c := startStdio(t, &Server{API: newStdioProbe(), Credential: "configured"})
	c.initialize(t)
	for _, id := range []string{"null", "true", "[]", "{}", "1.5"} {
		c.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":"wait"}}}`, id))
		if res := c.read(t); res.Error == nil || res.Error.Code != -32600 {
			t.Fatalf("invalid ID %s: %+v", id, res)
		}
	}
	c.finish(t)
}

type panickingStdioProbe struct{ *stdioProbe }

func (p *panickingStdioProbe) GetTask(ctx context.Context, credential, id string) (comitia.Task, error) {
	if id == "panic" {
		panic("private panic details: " + credential)
	}
	return p.stdioProbe.GetTask(ctx, credential, id)
}

func TestStdioToolPanicPreservesOtherCallsAndSanitizesLog(t *testing.T) {
	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousWriter)
	p := &panickingStdioProbe{newStdioProbe()}
	p.release["wait"] = make(chan struct{})
	p.release["ok"] = make(chan struct{})
	close(p.release["ok"])
	c := startStdio(t, &Server{API: p, Credential: "private-credential"})
	c.initialize(t)
	c.send(t, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":"wait"}}}`)
	probeEvent(t, p.started, "wait")
	c.send(t, `{"jsonrpc":"2.0","id":"private-request-id","method":"tools/call","params":{"name":"get_task","arguments":{"task_id":"panic"}}}`)
	res := c.read(t)
	if string(res.ID) != `"private-request-id"` || res.Error == nil || res.Error.Code != -32603 || res.Error.Message != "internal error" || len(res.Result) != 0 {
		t.Fatalf("panic response: %+v", res)
	}
	if !strings.Contains(logs.String(), "recovered panic in tool call") || strings.Contains(logs.String(), "private") {
		t.Fatalf("panic log missing or exposes secrets: %q", logs.String())
	}
	c.send(t, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_task","arguments":{"task_id":"ok"}}}`)
	if res := c.read(t); string(res.ID) != "2" || res.Error != nil || !strings.Contains(string(res.Result), `"isError":false`) {
		t.Fatalf("tool after panic: %+v", res)
	}
	c.send(t, `{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	if res := c.read(t); string(res.ID) != "3" || res.Error != nil {
		t.Fatalf("ping after panic: %+v", res)
	}
	close(p.release["wait"])
	if res := c.read(t); string(res.ID) != "1" || res.Error != nil {
		t.Fatalf("other in-flight call after panic: %+v", res)
	}
	c.finish(t)
}

func TestStdioEOFCancellationReportsCount(t *testing.T) {
	p := newStdioProbe()
	c := startStdio(t, &Server{API: p, Credential: "configured"})
	c.initialize(t)
	for i := 1; i <= 3; i++ {
		c.send(t, fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":%q}}}`, i, fmt.Sprint(i)))
		probeEvent(t, p.started, fmt.Sprint(i))
	}
	c.send(t, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1}}`)
	probeEvent(t, p.stopped, "1")
	_ = c.in.Close()
	select {
	case err := <-c.done:
		if err == nil || !strings.Contains(err.Error(), "EOF shutdown cancelled 2 tool calls") {
			t.Fatalf("EOF cancellation count: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("EOF did not shut down")
	}
	for res := range c.responses {
		t.Errorf("cancelled call responded: %+v", res)
	}
	stopped := map[string]bool{<-p.stopped: true, <-p.stopped: true}
	if !stopped["2"] || !stopped["3"] {
		t.Fatalf("calls stopped: %v", stopped)
	}
}
