package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

func resultCall(t *testing.T, s *Server, name string, args map[string]any) map[string]any {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	res := s.handle(context.Background(), request{JSONRPC: "2.0", Method: "tools/call", Params: params})
	if res.Error != nil {
		t.Fatalf("%s RPC error: %v", name, res.Error)
	}
	return res.Result.(map[string]any)
}

func resultText(result map[string]any) string {
	return result["content"].([]any)[0].(map[string]any)["text"].(string)
}

func TestAllToolsCompactPreference(t *testing.T) {
	for _, definition := range tools() {
		name := definition["name"].(string)
		for _, mode := range []string{"omitted", "null", "true", "false"} {
			t.Run(name+"/"+mode, func(t *testing.T) {
				s, args := schemaConformanceFixture(t, name)
				switch mode {
				case "omitted":
					delete(args, "compact")
				case "null":
					args["compact"] = nil
				case "true":
					args["compact"] = true
				case "false":
					args["compact"] = false
				}
				result := resultCall(t, s, name, args)
				if result["isError"] != false {
					t.Fatalf("%s tool failed", name)
				}
				text := resultText(result)
				structured, err := json.Marshal(result["structuredContent"])
				if err != nil {
					t.Fatal(err)
				}
				if mode == "false" {
					if text != string(structured) {
						t.Fatal("full text differs from structured result")
					}
				} else if len(text) > 512 {
					t.Fatalf("summary too large: %d bytes", len(text))
				}
			})
		}
	}
}

func TestSendAndTaskResultByteSizes(t *testing.T) {
	body := strings.Repeat("large body <\n", 1000)
	s := &Server{API: &fakeAPI{getTask: comitia.Task{ID: "t1", Body: body}, listTasks: []comitia.Task{{ID: "t1", Body: body}}}, Credential: "caller"}
	for _, tc := range []struct {
		name string
		args map[string]any
		want string
	}{
		{"send_message", map[string]any{"context_id": "c1", "dedupe_key": "key", "body": body}, "message id=m1; sequence=1"},
		{"get_task", map[string]any{"task_id": "t1"}, "task id=t1"},
		{"list_tasks", map[string]any{"context_id": "c1"}, "list_tasks: 1 items"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			compact := resultCall(t, s, tc.name, tc.args)
			text := resultText(compact)
			if len(text) > 200 || !strings.Contains(text, tc.want) || strings.Contains(text, "large body") {
				t.Fatalf("bad summary: %d bytes", len(text))
			}
			tc.args["compact"] = false
			full := resultCall(t, s, tc.name, tc.args)
			compactJSON, _ := json.Marshal(compact)
			fullJSON, _ := json.Marshal(full)
			compactStructured, _ := json.Marshal(compact["structuredContent"])
			fullStructured, _ := json.Marshal(full["structuredContent"])
			if string(compactStructured) != string(fullStructured) || !strings.Contains(string(compactStructured), "large body") {
				t.Fatal("compact changed or dropped the full result")
			}
			if len(compactJSON)*10 > len(fullJSON)*6 {
				t.Fatalf("insufficient reduction: compact=%d full=%d", len(compactJSON), len(fullJSON))
			}
			t.Logf("text=%d compact=%d full=%d bytes", len(text), len(compactJSON), len(fullJSON))
		})
	}
}

func TestCompactArgumentValidationAndErrors(t *testing.T) {
	f := &fakeAPI{}
	s := &Server{API: f, Credential: "caller"}
	for _, value := range []any{0, "false", []any{}, map[string]any{}} {
		result := resultCall(t, s, "get_task", map[string]any{"task_id": "t1", "compact": value})
		if result["isError"] != true || f.credential != "" {
			t.Fatal("invalid compact reached the API")
		}
	}
	result := resultCall(t, s, "get_task", map[string]any{"task_id": "t1", "compact": true, "surprise": true})
	if result["isError"] != true || f.credential != "" {
		t.Fatal("compact bypassed unknown argument validation")
	}
	for _, err := range []error{comitia.ErrNotFound, comitia.ErrForbidden} {
		f.err = err
		for _, compact := range []bool{true, false} {
			result := resultCall(t, s, "get_task", map[string]any{"task_id": "private-task", "compact": compact})
			if result["isError"] != true || strings.Contains(resultText(result), "private-task") {
				t.Fatal("error revealed private ID or changed into success")
			}
		}
	}
}

func TestDiscoveryPageSummaries(t *testing.T) {
	for _, name := range []string{"list_tasks", "list_participants"} {
		t.Run(name, func(t *testing.T) {
			s, args := schemaConformanceFixture(t, name)
			args["page"] = true
			delete(args, "compact")
			result := resultCall(t, s, name, args)
			if result["isError"] != false {
				t.Fatal("page failed")
			}
			text := resultText(result)
			if len(text) > 300 || !strings.Contains(text, "next_after=") || !strings.Contains(text, "has_more=false") {
				t.Fatalf("incomplete page summary: %s", text)
			}
			value := result["structuredContent"].(map[string]any)["result"]
			switch page := value.(type) {
			case comitia.TaskPage:
				if len(page.Tasks) == 0 || !strings.Contains(text, page.NextAfter) {
					t.Fatal("lost task page cursor or rows")
				}
			case comitia.ParticipantPage:
				if len(page.Participants) == 0 || !strings.Contains(text, page.NextAfter) {
					t.Fatal("lost participant page cursor or rows")
				}
			default:
				t.Fatalf("page shape changed: %T", value)
			}
		})
	}
}

func TestCompactEmptyReplayKeepsCursorAndClaimTokenInStructuredOnly(t *testing.T) {
	for _, name := range []string{"replay_messages", "wait_messages"} {
		args := map[string]any{"context_id": "c", "after": 42}
		if name == "wait_messages" {
			args["timeout_seconds"] = 0
		}
		result := resultCall(t, &Server{API: emptyCollectionsAPI{}, Credential: "caller"}, name, args)
		if !strings.Contains(resultText(result), "next_after=42") {
			t.Fatal("empty summary lost requested cursor")
		}
	}
	s, args := schemaConformanceFixture(t, "claim_task")
	delete(args, "compact")
	result := resultCall(t, s, "claim_task", args)
	claim := result["structuredContent"].(map[string]any)["result"].(comitia.TaskClaim)
	if claim.Token == "" || strings.Contains(resultText(result), claim.Token) {
		t.Fatal("claim token missing from structured result or duplicated into summary")
	}
}
