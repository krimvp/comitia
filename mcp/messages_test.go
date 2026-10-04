package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
)

func TestTypedMCPUsesRealService(t *testing.T) {
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "local"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "local", AdminName: "owner", SessionTTL: time.Hour})
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
	s := &Server{API: svc, Credential: owner.Token}
	listed, discoveryErr := s.call(ctx, "list_message_schemas", json.RawMessage(`{}`))
	if discoveryErr != nil {
		t.Fatalf("runtime schema discovery: %v", discoveryErr)
	}
	encoded, _ := json.Marshal(listed)
	var descriptors []map[string]any
	if err := json.Unmarshal(encoded, &descriptors); err != nil || len(descriptors) != 11 {
		t.Fatalf("descriptors: %s %v", encoded, err)
	}
	gotSchema, discoveryErr := s.call(ctx, "get_message_schema", json.RawMessage(`{"schema":"comitia.review-request.v1"}`))
	if discoveryErr != nil {
		t.Fatal(discoveryErr)
	}
	encoded, _ = json.Marshal(gotSchema)
	var descriptor map[string]any
	_ = json.Unmarshal(encoded, &descriptor)
	if descriptor["task_required"] != true || descriptor["payload_schema"] == nil || len(descriptor["examples"].([]any)) == 0 {
		t.Fatalf("incomplete descriptor: %s", encoded)
	}
	v, err := s.call(ctx, "list_participants", json.RawMessage(`{"context_id":"`+c.ID+`"}`))
	if err != nil || len(v.([]comitia.Participant)) != 1 {
		t.Fatalf("discovery: %v %v", v, err)
	}
	raw := json.RawMessage(`{"context_id":"` + c.ID + `","dedupe_key":"x","body":"hello","agent_instance":{"id":"worker","label":"MCP worker"},"envelope":{"schema":"comitia.discussion.v1","recipient_instances":["reviewer"],"payload":{}}}`)
	v, err = s.call(ctx, "send_message", raw)
	if err != nil || v.(comitia.Message).Envelope == nil {
		t.Fatalf("typed send: %v %v", v, err)
	}
	message := v.(comitia.Message)
	replay, err := s.call(ctx, "replay_messages", json.RawMessage(`{"context_id":"`+c.ID+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	messages := replay.([]comitia.Message)
	if len(messages) != 1 || messages[0].AgentInstance == nil || messages[0].AgentInstance.ID != "worker" || messages[0].AgentInstance.Label != "MCP worker" || !messages[0].AgentInstanceSelfReported || len(messages[0].Envelope.RecipientInstances) != 1 {
		t.Fatalf("MCP lost claim: %v", messages)
	}
	ack, _ := json.Marshal(map[string]any{"context_id": c.ID, "sequence": message.Sequence, "agent_instance": "worker"})
	if _, err = s.call(ctx, "ack_messages", ack); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		instance string
		want     int64
	}{{"worker", message.Sequence}, {"reviewer", 0}, {"", 0}} {
		args, _ := json.Marshal(map[string]any{"context_id": c.ID, "agent_instance": check.instance})
		result, err := s.call(ctx, "message_cursor", args)
		if err != nil || result.(map[string]int64)["sequence"] != check.want {
			t.Fatalf("MCP cursor %q: %v %v", check.instance, result, err)
		}
	}
	_, other, _, err := svc.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "other", TTL: 45 * time.Minute, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(30 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "compound-other", Body: "other session", Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{"reviewer"}, RecipientSessionIDs: []string{other.Session.ID}, Payload: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"replay_messages", "wait_messages"} {
		for _, page := range []bool{false, true} {
			args, _ := json.Marshal(map[string]any{"context_id": c.ID, "for_instance": "reviewer", "strict_recipients": true, "timeout_seconds": 0, "page": page})
			if tool == "replay_messages" {
				args, _ = json.Marshal(map[string]any{"context_id": c.ID, "for_instance": "reviewer", "strict_recipients": true, "page": page})
			}
			value, err := s.call(ctx, tool, args)
			if err != nil {
				t.Fatal(err)
			}
			var selected []comitia.Message
			if page {
				p := value.(comitia.MessagePage)
				selected = p.Messages
				if p.HasMore {
					t.Fatal("mismatched session in lookahead")
				}
			} else {
				selected = value.([]comitia.Message)
			}
			if len(selected) != 1 || selected[0].ID != message.ID {
				t.Fatalf("MCP strict %s page=%t: %v", tool, page, selected)
			}
		}
	}
	s.Credential = "invalid"
	if _, err := s.call(ctx, "list_message_schemas", json.RawMessage(`{}`)); !errors.Is(err, comitia.ErrUnauthenticated) {
		t.Fatalf("MCP discovery auth: %v", err)
	}
	s.Credential = owner.Token
	if _, err := s.call(ctx, "get_message_schema", json.RawMessage(`{"schema":"unknown.v1"}`)); !errors.Is(err, comitia.ErrNotFound) {
		t.Fatalf("MCP unknown schema: %v", err)
	}
	bad := json.RawMessage(`{"context_id":"` + c.ID + `","dedupe_key":"bad","body":"hello","envelope":{"schema":"comitia.discussion.v1","from":"fake","payload":{}}}`)
	if _, err = s.call(ctx, "send_message", bad); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("spoof field: %v", err)
	}
	if _, err = s.call(ctx, "list_participants", json.RawMessage(`{"context_id":"`+c.ID+`","limit":0}`)); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("zero limit: %v", err)
	}
}

func TestSchemaDiscoveryLegacyAPI(t *testing.T) {
	s := &Server{API: &fakeAPI{}, Credential: "fixed"}
	for _, tool := range []string{"list_message_schemas", "get_message_schema"} {
		raw := json.RawMessage(`{}`)
		if tool == "get_message_schema" {
			raw = json.RawMessage(`{"schema":"comitia.discussion.v1"}`)
		}
		if _, err := s.call(context.Background(), tool, raw); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("unsupported %s: %v", tool, err)
		}
	}
	for _, tc := range []struct{ tool, raw string }{{"list_message_schemas", `{"schema":"x"}`}, {"get_message_schema", `{}`}, {"get_message_schema", `{"schema":"x","extra":true}`}} {
		if _, err := s.call(context.Background(), tc.tool, json.RawMessage(tc.raw)); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("strict discovery arguments: %v", err)
		}
	}
}
