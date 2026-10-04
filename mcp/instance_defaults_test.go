package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

type instanceAPI struct {
	comitia.API
	messages []comitia.MessageInput
	acks     []string
	cursors  []string
	claims   []comitia.AgentInstance
	reads    []string
	resumes  []comitia.AgentInstance
}

func (a *instanceAPI) read(filters []comitia.MessageFilters) {
	instance := "<nil>"
	if filters[0].AgentInstance != nil {
		instance = *filters[0].AgentInstance
	}
	a.reads = append(a.reads, instance)
}

func (a *instanceAPI) ReplayMessages(_ context.Context, _, _ string, _ int64, _ int, filters ...comitia.MessageFilters) ([]comitia.Message, error) {
	a.read(filters)
	return []comitia.Message{}, nil
}

func (a *instanceAPI) WaitMessages(_ context.Context, _, _ string, _ int64, _ time.Duration, filters ...comitia.MessageFilters) ([]comitia.Message, error) {
	a.read(filters)
	return []comitia.Message{}, nil
}

func (a *instanceAPI) SetInstanceResume(_ context.Context, _, _ string, instance *comitia.AgentInstance, _ json.RawMessage) error {
	a.resumes = append(a.resumes, *instance)
	return nil
}

func (a *instanceAPI) AppendMessage(_ context.Context, _, _ string, in comitia.MessageInput) (comitia.Message, error) {
	a.messages = append(a.messages, in)
	return comitia.Message{AgentInstance: in.AgentInstance}, nil
}

func (a *instanceAPI) AckMessages(_ context.Context, _, _ string, _ int64, instances ...string) error {
	a.acks = append(a.acks, instances[0])
	return nil
}

func (a *instanceAPI) MessageCursor(_ context.Context, _, _ string, instances ...string) (int64, error) {
	a.cursors = append(a.cursors, instances[0])
	return 0, nil
}

func (a *instanceAPI) ClaimTask(_ context.Context, _, _ string, _ time.Duration, instances ...comitia.AgentInstance) (comitia.TaskClaim, error) {
	a.claims = append(a.claims, instances...)
	return comitia.TaskClaim{}, nil
}

func TestMCPInstanceDefaultsOnBothTransports(t *testing.T) {
	t.Setenv("COMITIA_AGENT_INSTANCE", "worker")
	t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "Worker")
	defaults, err := comitia.InstanceDefaultsFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	requests := []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"send_message","arguments":{"context_id":"c","dedupe_key":"started","body":"ready"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"send_message","arguments":{"context_id":"c","dedupe_key":"started","body":"ready","agent_instance":{"id":"explicit"}}}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ack_messages","arguments":{"context_id":"c","sequence":1}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"ack_messages","arguments":{"context_id":"c","sequence":1,"agent_instance":"explicit"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"message_cursor","arguments":{"context_id":"c"}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"message_cursor","arguments":{"context_id":"c","agent_instance":"explicit"}}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"message_cursor","arguments":{"context_id":"c","agent_instance":""}}}`,
		`{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"ack_messages","arguments":{"context_id":"c","sequence":1,"agent_instance":""}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"claim_task","arguments":{"task_id":"t"}}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"claim_task","arguments":{"task_id":"t","agent_instance":{"id":"explicit"}}}}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"claim_task","arguments":{"task_id":"t","agent_instance":null}}}`,
		`{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"replay_messages","arguments":{"context_id":"c"}}}`,
		`{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"replay_messages","arguments":{"context_id":"c","agent_instance":"explicit"}}}`,
		`{"jsonrpc":"2.0","id":11,"method":"tools/call","params":{"name":"replay_messages","arguments":{"context_id":"c","agent_instance":""}}}`,
		`{"jsonrpc":"2.0","id":12,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":"c","timeout_seconds":0}}}`,
		`{"jsonrpc":"2.0","id":13,"method":"tools/call","params":{"name":"wait_messages","arguments":{"context_id":"c","timeout_seconds":0,"agent_instance":""}}}`,
		`{"jsonrpc":"2.0","id":14,"method":"tools/call","params":{"name":"set_instance_resume","arguments":{"context_id":"c","resume":{}}}}`,
		`{"jsonrpc":"2.0","id":15,"method":"tools/call","params":{"name":"set_instance_resume","arguments":{"context_id":"c","resume":{},"agent_instance":{"id":"explicit"}}}}`,
	}
	for _, transport := range []string{"stdio", "http"} {
		t.Run(transport, func(t *testing.T) {
			api := &instanceAPI{}
			if transport == "stdio" {
				client := startStdio(t, &Server{API: api, Credential: "token", InstanceDefaults: defaults})
				client.initialize(t)
				for _, req := range requests {
					client.send(t, req)
					res := client.read(t)
					if res.Error != nil || strings.Contains(string(res.Result), `"isError":true`) {
						t.Fatalf("tool failure: %+v", res)
					}
				}
				client.finish(t)
			} else {
				h := NewHTTPHandler(api, HTTPConfig{InstanceDefaults: defaults})
				for _, req := range requests {
					res := post(t, h, req, map[string]string{"Authorization": "Bearer token"})
					if res.Code != 200 || toolError(t, res.Body.String()) {
						t.Fatalf("tool failure: %s", res.Body.String())
					}
				}
			}
			wantClaims := []comitia.AgentInstance{{ID: "worker", Label: "Worker"}, {ID: "explicit"}, {ID: "worker", Label: "Worker"}}
			if len(api.claims) != len(wantClaims) {
				t.Fatalf("claims: %v", api.claims)
			}
			for i, want := range wantClaims {
				if api.claims[i] != want {
					t.Fatalf("claim %d: %v want=%v", i, api.claims[i], want)
				}
			}
			if len(api.messages) != 2 || *api.messages[0].AgentInstance != (comitia.AgentInstance{ID: "worker", Label: "Worker"}) || *api.messages[1].AgentInstance != (comitia.AgentInstance{ID: "explicit"}) {
				t.Fatalf("message defaults: %v", api.messages)
			}
			if strings.Join(api.acks, ",") != "worker,explicit," || strings.Join(api.cursors, ",") != "worker,explicit," {
				t.Fatalf("cursor defaults: ack=%v cursor=%v", api.acks, api.cursors)
			}
			if strings.Join(api.reads, ",") != "worker,explicit,<nil>,worker,<nil>" {
				t.Fatalf("presence read defaults: %v", api.reads)
			}
			if len(api.resumes) != 2 || api.resumes[0] != (comitia.AgentInstance{ID: "worker", Label: "Worker"}) || api.resumes[1] != (comitia.AgentInstance{ID: "explicit"}) {
				t.Fatalf("resume defaults: %v", api.resumes)
			}
		})
	}
}

func TestInstanceToolDescriptions(t *testing.T) {
	for _, name := range []string{"send_message", "claim_task", "ack_messages", "message_cursor", "replay_messages", "wait_messages", "set_instance_resume"} {
		found := false
		for _, tool := range tools() {
			if tool["name"] == name {
				b, _ := json.Marshal(tool)
				if !strings.Contains(string(b), "COMITIA_AGENT_INSTANCE") {
					t.Fatalf("%s does not describe default", name)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("missing tool %s", name)
		}
	}
}
