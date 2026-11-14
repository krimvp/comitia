package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestScopedMessageTools(t *testing.T) {
	for _, remote := range []bool{false, true} {
		s, svc, owner, c := contextServer(t, remote)
		ctx := context.Background()
		root, err := svc.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "root", Body: "root"})
		if err != nil {
			t.Fatal(err)
		}
		reply, err := svc.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "reply", Body: "reply", Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", ReplyTo: root.ID, Payload: json.RawMessage(`{}`)}})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"context_id": c.ID, "message_id": reply.ID})
		value, err := s.call(ctx, "get_message", raw)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := value.(comitia.Message)
		if !ok || got.ID != reply.ID || got.SessionID != owner.Session.ID {
			t.Fatalf("get: %+v", value)
		}
		for _, tool := range []string{"replay_messages", "wait_messages"} {
			args := map[string]any{"context_id": c.ID, "reply_to": root.ID, "thread_id": root.ID, "from_session": owner.Session.ID}
			if tool == "wait_messages" {
				args["timeout_seconds"] = 0
			}
			raw, _ = json.Marshal(args)
			value, err = s.call(ctx, tool, raw)
			if err != nil {
				t.Fatal(err)
			}
			messages, ok := value.([]comitia.Message)
			if !ok || len(messages) != 1 || messages[0].ID != reply.ID {
				t.Fatalf("%s: %+v", tool, value)
			}
		}
		s.API = legacyDiscoveryAPI{s.API}
		raw, _ = json.Marshal(map[string]any{"context_id": c.ID, "reply_to": root.ID})
		if _, err := s.call(ctx, "replay_messages", raw); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("legacy API widened scoped read: %v", err)
		}
		if _, err := s.call(ctx, "get_message", json.RawMessage(`{"context_id":"c","message_id":"m"}`)); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("optional capability: %v", err)
		}
	}
}

func TestScopedQueryPublishedSchemas(t *testing.T) {
	for _, tool := range messageTools() {
		name := tool["name"].(string)
		if name != "get_message" && name != "replay_messages" && name != "wait_messages" {
			continue
		}
		compiler := jsonschema.NewCompiler()
		url := "urn:test:" + name
		raw, _ := json.Marshal(tool["inputSchema"])
		var advertised any
		_ = json.Unmarshal(raw, &advertised)
		if err := compiler.AddResource(url, advertised); err != nil {
			t.Fatal(err)
		}
		schema, err := compiler.Compile(url)
		if err != nil {
			t.Fatal(err)
		}
		args := map[string]any{"context_id": "c"}
		if name == "get_message" {
			args["message_id"] = "m"
		} else {
			args["reply_to"] = "m"
			args["thread_id"] = "m"
			args["from_session"] = "s"
		}
		if err := schema.Validate(args); err != nil {
			t.Fatalf("%s contract: %v", name, err)
		}
		if name != "get_message" {
			for _, field := range []string{"reply_to", "thread_id", "from_session"} {
				args[field] = nil
			}
			if err := schema.Validate(args); err != nil {
				t.Fatalf("server accepts omitted/null filter: %v", err)
			}
		}
	}
}
