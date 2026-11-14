package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/krimvp/comitia/pkg/httpapi"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

func TestMessageReadOptionsLocalAndRemoteMCP(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "remote"}[remote], func(t *testing.T) {
			s, args := schemaConformanceFixture(t, "replay_messages")
			if remote {
				s = remoteSchemaServer(t, s)
			}
			ctx := context.Background()
			cid := args["context_id"].(string)
			own, err := s.API.AppendMessage(ctx, s.Credential, cid, comitia.MessageInput{DedupeKey: "own-read", Body: "own", AgentInstance: &comitia.AgentInstance{ID: "schema-worker"}})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.API.AckMessages(ctx, s.Credential, cid, own.Sequence, "schema-worker"); err != nil {
				t.Fatal(err)
			}
			peer, err := s.API.AppendMessage(ctx, s.Credential, cid, comitia.MessageInput{DedupeKey: "peer-read", Body: "peer", AgentInstance: &comitia.AgentInstance{ID: "peer"}})
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"replay_messages", "wait_messages"} {
				for _, page := range []bool{false, true} {
					for _, compact := range []any{nil, false, true} {
						a := map[string]any{"context_id": cid, "from_cursor": true, "exclude_self": true, "agent_instance": "schema-worker", "page": page, "compact": compact}
						if name == "wait_messages" {
							a["timeout_seconds"] = 0
						}
						result := resultCall(t, s, name, a)
						if result["isError"] != false {
							t.Fatalf("read failed: %v", result)
						}
						value := result["structuredContent"].(map[string]any)["result"]
						var messages []comitia.Message
						if page {
							messages = value.(comitia.MessagePage).Messages
						} else {
							messages = value.([]comitia.Message)
						}
						if len(messages) != 1 || messages[0].ID != peer.ID {
							t.Fatalf("cursor start lost peer: %v", messages)
						}
						if compact != false && !strings.Contains(resultText(result), "next_after=") {
							t.Fatalf("compact cursor missing: %v", result)
						}
						// Empty cursor-start reads still expose the resolved cursor.
						a["from_instance"] = "missing"
						empty := resultCall(t, s, name, a)
						if empty["isError"] != false || compact != false && !strings.Contains(resultText(empty), fmt.Sprintf("next_after=%d;", own.Sequence)) {
							t.Fatalf("empty cursor missing: %v", empty)
						}
					}
				}
				ambiguous := map[string]any{"context_id": cid, "from_cursor": true, "after": 0}
				if name == "wait_messages" {
					ambiguous["timeout_seconds"] = 0
				}
				if result := resultCall(t, s, name, ambiguous); result["isError"] != true {
					t.Fatalf("ambiguous start accepted: %v", result)
				}
			}
			if cursor, err := s.API.MessageCursor(ctx, s.Credential, cid, "schema-worker"); err != nil || cursor != own.Sequence {
				t.Fatalf("read acknowledged peer: %d %v", cursor, err)
			}
		})
	}
}

func TestMCPRenewalDefaultsAndExplicitSchemas(t *testing.T) {
	s, args := schemaConformanceFixture(t, "replay_messages")
	ctx := context.Background()
	cid := args["context_id"].(string)
	task, err := s.API.CreateTask(ctx, s.Credential, cid, comitia.TaskInput{Title: "renewal"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.API.ClaimTask(ctx, s.Credential, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := s.API.ReplayMessages(ctx, s.Credential, cid, 0, 100, comitia.MessageFilters{Schema: comitia.TaskEventSchema})
	if err != nil {
		t.Fatal(err)
	}
	after := messages[len(messages)-1].Sequence
	if _, err = s.API.RenewClaim(ctx, s.Credential, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"replay_messages", "wait_messages"} {
		a := map[string]any{"context_id": cid, "after": after, "page": true}
		if name == "wait_messages" {
			a["timeout_seconds"] = 0
		}
		result := resultCall(t, s, name, a)
		if result["isError"] != false {
			t.Fatalf("read failed: %v", result)
		}
		page := result["structuredContent"].(map[string]any)["result"].(comitia.MessagePage)
		if len(page.Messages) != 0 || page.ScannedThrough != after+1 {
			t.Fatalf("renewal scan stalled: %+v", page)
		}
		for _, field := range []string{"schema", "schemas"} {
			if field == "schema" {
				a[field] = comitia.TaskEventSchema
			} else {
				delete(a, "schema")
				a[field] = []string{"comitia.discussion.v1", comitia.TaskEventSchema}
			}
			result = resultCall(t, s, name, a)
			if result["isError"] != false {
				t.Fatalf("explicit read failed: %v", result)
			}
			page = result["structuredContent"].(map[string]any)["result"].(comitia.MessagePage)
			if len(page.Messages) != 1 {
				t.Fatalf("renewal opt-in missing: %+v", page)
			}
			var p comitia.TaskEventPayload
			if err = json.Unmarshal(page.Messages[0].Envelope.Payload, &p); err != nil || p.Kind != "renewed" {
				t.Fatalf("renewal: %+v %v", p, err)
			}
		}
	}
}

func remoteSchemaServer(t *testing.T, s *Server) *Server {
	t.Helper()
	ts := httptest.NewServer(httpapi.NewHandler(s.API))
	t.Cleanup(ts.Close)
	client, err := httpapi.NewClientWithInstanceDefaults(ts.URL, ts.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	copy := *s
	copy.API = client
	return &copy
}
