package mcp

import (
	"context"
	"encoding/json"
	"github.com/krimvp/comitia/pkg/comitia"
	"testing"
	"time"
)

type emptyCollectionsAPI struct{ comitia.API }

func (emptyCollectionsAPI) ListContexts(context.Context, string, string, int) ([]comitia.WorkContext, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ListChildContexts(context.Context, string, string, string, int) ([]comitia.WorkContext, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ListParticipants(context.Context, string, string, int) ([]comitia.Participant, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ListTasks(context.Context, string, string, comitia.TaskStatus, int) ([]comitia.Task, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ListInstances(context.Context, string, string) ([]comitia.InstancePresence, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ReplayMessages(context.Context, string, string, int64, int, ...comitia.MessageFilters) ([]comitia.Message, error) {
	return nil, nil
}
func (emptyCollectionsAPI) WaitMessages(context.Context, string, string, int64, time.Duration, ...comitia.MessageFilters) ([]comitia.Message, error) {
	return nil, nil
}

func TestEveryMCPEmptyCollectionIsArray(t *testing.T) {
	s := &Server{API: emptyCollectionsAPI{}, Credential: "caller"}
	for _, tc := range []struct{ name, args string }{{"list_contexts", `{}`}, {"list_child_contexts", `{"parent_context_id":"c"}`}, {"list_participants", `{"context_id":"c"}`}, {"list_tasks", `{"context_id":"c"}`}, {"list_instances", `{"context_id":"c"}`}, {"replay_messages", `{"context_id":"c"}`}, {"wait_messages", `{"context_id":"c","timeout_seconds":0}`}} {
		t.Run(tc.name, func(t *testing.T) {
			params := json.RawMessage(`{"name":"` + tc.name + `","arguments":` + tc.args + `}`)
			response := s.handle(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
			if response.Error != nil {
				t.Fatal(response.Error)
			}
			result := response.Result.(map[string]any)
			if result["isError"] != false {
				t.Fatalf("tool error: %+v", result)
			}
			raw, err := json.Marshal(result["structuredContent"])
			if err != nil || string(raw) != `{"result":[]}` {
				t.Fatalf("empty: %s %v", raw, err)
			}
			content := result["content"].([]any)[0].(map[string]any)["text"]
			if len(content.(string)) > 200 || content == `{"result":[]}` {
				t.Fatalf("expected short collection summary: %v", content)
			}
		})
	}
}
