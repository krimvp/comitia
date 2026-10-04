package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
)

func TestRemoteMCPMessagePagesAndCompactResults(t *testing.T) {
	ctx := context.Background()
	st, err := comitia.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "boot"})
	if err != nil {
		t.Fatal(err)
	}
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "boot", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, owner.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	wc, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, err = svc.AppendMessage(ctx, owner.Token, wc.ID, comitia.MessageInput{DedupeKey: fmt.Sprint(i), Body: strings.Repeat("<\n", 125000)})
		if err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(httpapi.NewHandler(svc))
	defer ts.Close()
	client, err := httpapi.NewClientWithInstanceDefaults(ts.URL, ts.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{API: client, Credential: owner.Token}
	for _, name := range []string{"replay_messages", "wait_messages"} {
		t.Run(name, func(t *testing.T) {
			for _, compact := range []bool{false, true} {
				args := map[string]any{"context_id": wc.ID, "max_bytes": comitia.MaxMessageBytes, "page": true, "compact": compact}
				if name == "wait_messages" {
					args["timeout_seconds"] = 0
				}
				params, _ := json.Marshal(map[string]any{"name": name, "arguments": args})
				res := s.handle(ctx, request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
				if res.Error != nil {
					t.Fatal(res.Error)
				}
				result := res.Result.(map[string]any)
				if result["isError"] != false {
					t.Fatalf("tool failed: %v", result)
				}
				page := result["structuredContent"].(map[string]any)["result"].(comitia.MessagePage)
				if len(page.Messages) != 3 || page.NextAfter != 3 || page.HasMore {
					t.Fatalf("lost remote page: count=%d after=%d", len(page.Messages), page.NextAfter)
				}
				text := result["content"].([]any)[0].(map[string]any)["text"].(string)
				structured, _ := json.Marshal(result["structuredContent"])
				if compact {
					if len(text) > 200 || strings.Contains(text, "Body") || !strings.Contains(text, "next_after=3") {
						t.Fatalf("not compact: %d bytes", len(text))
					}
				} else if text != string(structured) {
					t.Fatal("legacy full-text result changed")
				}
			}
			// Compact output also preserves the default legacy array shape.
			argsLegacy := map[string]any{"context_id": wc.ID, "max_bytes": comitia.MaxMessageBytes, "compact": true}
			if name == "wait_messages" {
				argsLegacy["timeout_seconds"] = 0
			}
			paramsLegacy, _ := json.Marshal(map[string]any{"name": name, "arguments": argsLegacy})
			legacyRes := s.handle(ctx, request{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/call", Params: paramsLegacy})
			if legacyRes.Error != nil {
				t.Fatal(legacyRes.Error)
			}
			legacyResult := legacyRes.Result.(map[string]any)
			legacyMessages := legacyResult["structuredContent"].(map[string]any)["result"].([]comitia.Message)
			legacyText := legacyResult["content"].([]any)[0].(map[string]any)["text"].(string)
			if len(legacyMessages) != 3 || len(legacyText) > 200 || !strings.Contains(legacyText, "last_sequence=3") {
				t.Fatal("compact legacy array changed or duplicated")
			}
			// Small pages carry a recoverable continuation through remote MCP too.
			args := map[string]any{"context_id": wc.ID, "max_bytes": 1, "page": true}
			if name == "wait_messages" {
				args["timeout_seconds"] = 0
			}
			raw, _ := json.Marshal(args)
			value, err := s.call(ctx, name, raw)
			if err != nil {
				t.Fatal(err)
			}
			page := value.(comitia.MessagePage)
			if len(page.Messages) != 1 || page.NextAfter != 1 || !page.HasMore {
				t.Fatalf("small page: count=%d after=%d more=%v", len(page.Messages), page.NextAfter, page.HasMore)
			}
		})
	}
	for _, definition := range messageTools() {
		name := definition["name"]
		if name != "replay_messages" && name != "wait_messages" {
			continue
		}
		props := definition["inputSchema"].(map[string]any)["properties"].(map[string]any)
		for _, field := range []string{"max_bytes", "page", "compact"} {
			if props[field] == nil {
				t.Fatalf("%s omits %s", name, field)
			}
		}
	}
}

// A malformed embedded result must produce a protocol error, never empty text
// accompanied by structured content which cannot be encoded.
type invalidMessageResult struct{ comitia.API }

func (invalidMessageResult) ReplayMessages(context.Context, string, string, int64, int, ...comitia.MessageFilters) ([]comitia.Message, error) {
	return []comitia.Message{{Envelope: &comitia.MessageEnvelope{Payload: json.RawMessage(`{`)}}}, nil
}
func TestMCPMessageResultEncodingFailure(t *testing.T) {
	s := &Server{API: invalidMessageResult{}, Credential: "token"}
	res := s.handle(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: json.RawMessage(`{"name":"replay_messages","arguments":{"context_id":"context"}}`)})
	if res.Error == nil || res.Error.Code != -32603 {
		t.Fatalf("encoding failure not handled: %+v", res)
	}
	if _, err := json.Marshal(res); err != nil {
		t.Fatalf("error response cannot be encoded: %v", err)
	}
}
