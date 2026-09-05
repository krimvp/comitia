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
