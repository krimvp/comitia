package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/krimvp/comitia"
)

type fakeAPI struct {
	comitia.API
	credential  string
	appendCalls int
	err         error
	getTask     comitia.Task
	listTasks   []comitia.Task
	listStatus  comitia.TaskStatus
	listLimit   int
}

func (f *fakeAPI) GetTask(_ context.Context, credential, id string) (comitia.Task, error) {
	f.credential = credential
	if f.err != nil {
		return comitia.Task{}, f.err
	}
	t := f.getTask
	if t.ID == "" {
		t.ID = id
	}
	return t, nil
}
func (f *fakeAPI) ListTasks(_ context.Context, credential, cid string, status comitia.TaskStatus, limit int) ([]comitia.Task, error) {
	f.credential = credential
	f.listStatus = status
	f.listLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	return f.listTasks, nil
}

func (f *fakeAPI) AppendMessage(_ context.Context, credential, contextID string, in comitia.MessageInput) (comitia.Message, error) {
	f.credential = credential
	f.appendCalls++
	if f.err != nil {
		return comitia.Message{}, f.err
	}
	return comitia.Message{ID: "m1", ContextID: contextID, DedupeKey: in.DedupeKey, Body: in.Body, Sequence: 1}, nil
}

func (f *fakeAPI) MessageCursor(_ context.Context, credential, contextID string) (int64, error) {
	f.credential = credential
	if contextID != "c1" {
		return 0, comitia.ErrNotFound
	}
	return 7, nil
}

func serve(t *testing.T, api comitia.API, input string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := (&Server{API: api, Credential: "fixed-secret"}).Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	d := json.NewDecoder(&out)
	for {
		var v map[string]any
		err := d.Decode(&v)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	return got
}

func TestProtocolInitializeListAndCall(t *testing.T) {
	f := &fakeAPI{}
	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"send_message","arguments":{"context_id":"c1","dedupe_key":"k1","body":"hello"}}}`,
	}, "\n") + "\n"
	got := serve(t, f, input)
	if len(got) != 3 {
		t.Fatalf("responses=%d, want 3", len(got))
	}
	initResult := got[0]["result"].(map[string]any)
	if initResult["protocolVersion"] != ProtocolVersion {
		t.Fatalf("protocolVersion=%v", initResult["protocolVersion"])
	}
	b, _ := json.Marshal(got[1])
	if bytes.Contains(b, []byte("credential")) {
		t.Fatalf("tools/list exposes credential: %s", b)
	}
	if f.credential != "fixed-secret" || f.appendCalls != 1 {
		t.Fatalf("credential=%q calls=%d", f.credential, f.appendCalls)
	}
	call := got[2]["result"].(map[string]any)
	if call["isError"] != false {
		t.Fatalf("call result=%v", call)
	}
	if _, ok := call["structuredContent"].(map[string]any); !ok {
		t.Fatalf("structuredContent must be an object: %T", call["structuredContent"])
	}
}

func TestProtocolErrorsAndArgumentRejection(t *testing.T) {
	f := &fakeAPI{}
	got := serve(t, f, "{bad json\n"+`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"+`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"send_message","arguments":{"context_id":"c","dedupe_key":"k","body":"b","credential":"attacker"}}}`+"\n")
	if len(got) != 3 || got[0]["id"] != nil {
		t.Fatalf("parse response=%v", got)
	}
	result := got[2]["result"].(map[string]any)
	if result["isError"] != true {
		t.Fatalf("result=%v", result)
	}
	if f.appendCalls != 0 {
		t.Fatal("API called with an undeclared argument")
	}
}

func TestInternalErrorsAreSanitized(t *testing.T) {
	f := &fakeAPI{err: errors.New("database password leaked")}
	got := serve(t, f, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"+`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"send_message","arguments":{"context_id":"c","dedupe_key":"k","body":"b"}}}`+"\n")
	b, _ := json.Marshal(got)
	if bytes.Contains(b, []byte("password")) || !bytes.Contains(b, []byte("internal error")) {
		t.Fatalf("response=%s", b)
	}
}

func TestMessageCursorTool(t *testing.T) {
	f := &fakeAPI{}
	got := serve(t, f, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"+`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"message_cursor","arguments":{"context_id":"c1"}}}`+"\n")
	result := got[1]["result"].(map[string]any)
	structured := result["structuredContent"].(map[string]any)
	value := structured["result"].(map[string]any)
	if value["sequence"] != float64(7) || f.credential != "fixed-secret" {
		t.Fatalf("result=%v credential=%q", result, f.credential)
	}
}

func TestTaskToolsValidationAndDefaults(t *testing.T) {
	f := &fakeAPI{getTask: comitia.Task{ID: "t1", ContextID: "c1", Title: "safe"}, listTasks: []comitia.Task{{ID: "t1"}}}
	s := &Server{API: f, Credential: "fixed-secret"}
	v, err := s.call(context.Background(), "get_task", json.RawMessage(`{"task_id":"t1"}`))
	if err != nil || v.(comitia.Task).ID != "t1" {
		t.Fatalf("get: %v %v", v, err)
	}
	if _, err = s.call(context.Background(), "get_task", json.RawMessage(`{"task_id":""}`)); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("missing id: %v", err)
	}
	if _, err = s.call(context.Background(), "list_tasks", json.RawMessage(`{"context_id":"c1","status":"completed"}`)); err != nil || f.listStatus != comitia.TaskCompleted || f.listLimit != 100 {
		t.Fatalf("list: %v status=%v limit=%d", err, f.listStatus, f.listLimit)
	}
	for _, raw := range []string{`{"context_id":"c1","status":"bogus"}`, `{"context_id":"c1","limit":-1}`, `{"context_id":"c1","limit":1001}`, `{"context_id":"c1","limit":0}`, `{"context_id":"c1","credential":"x"}`} {
		if _, err = s.call(context.Background(), "list_tasks", json.RawMessage(raw)); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("raw=%s err=%v", raw, err)
		}
	}
	f.err = comitia.ErrForbidden
	if _, err = s.call(context.Background(), "get_task", json.RawMessage(`{"task_id":"t1"}`)); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("forbidden=%v", err)
	}
}

func TestTaskToolsProtocolAndSafeOutput(t *testing.T) {
	f := &fakeAPI{getTask: comitia.Task{ID: "t1", LeaseTokenHash: "hidden-lease-hash"}, listTasks: []comitia.Task{{ID: "t1", LeaseTokenHash: "hidden-lease-hash"}}}
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_task","arguments":{"task_id":"t1"}}}` + "\n" +
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_tasks","arguments":{"context_id":"c1"}}}` + "\n"
	got := serve(t, f, input)
	if len(got) != 3 {
		t.Fatalf("responses: %d", len(got))
	}
	for _, response := range got[1:] {
		result := response["result"].(map[string]any)
		if result["isError"] != false {
			t.Fatalf("tool failed: %v", result)
		}
		if _, ok := result["structuredContent"].(map[string]any); !ok {
			t.Fatal("non-object structured content")
		}
	}
	b, _ := json.Marshal(got)
	if bytes.Contains(b, []byte("hidden-lease-hash")) || bytes.Contains(b, []byte("fixed-secret")) {
		t.Fatal("secret in response")
	}
	s := &Server{API: f, Credential: "fixed-secret"}
	for _, name := range []string{"get_task", "list_tasks"} {
		if _, err := s.call(context.Background(), name, json.RawMessage(`{}`)); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("missing arguments for %s: %v", name, err)
		}
	}
	f.err = comitia.ErrForbidden
	if _, err := s.call(context.Background(), "list_tasks", json.RawMessage(`{"context_id":"c1"}`)); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("forbidden list: %v", err)
	}
}
