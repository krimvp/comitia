package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/krimvp/comitia"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestMCPSendTopLevelRecipients(t *testing.T) {
	s, args := schemaConformanceFixture(t, "send_message")
	delete(args, "envelope")
	delete(args, "task_fence")
	delete(args, "compact")
	// Get the authenticated session from discovery; names are never routing IDs.
	ps, err := s.API.ListParticipants(context.Background(), s.Credential, args["context_id"].(string), 100)
	if err != nil {
		t.Fatal(err)
	}
	args["recipient_session_ids"] = []any{ps[0].SessionID}
	args["recipient_instances"] = []any{"z", "a"}
	responses := serve(t, &fakeAPI{}, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"+`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")
	var definition map[string]any
	for _, v := range responses[1]["result"].(map[string]any)["tools"].([]any) {
		d := v.(map[string]any)
		if d["name"] == "send_message" {
			definition = d
		}
	}
	for _, draft := range []*jsonschema.Draft{jsonschema.Draft7, jsonschema.Draft2020} {
		compiler := jsonschema.NewCompiler()
		compiler.DefaultDraft(draft)
		if err = compiler.AddResource("https://comitia.test/send", definition["inputSchema"]); err != nil {
			t.Fatal(err)
		}
		schema, err := compiler.Compile("https://comitia.test/send")
		if err != nil {
			t.Fatal(err)
		}
		if err = schema.Validate(args); err != nil {
			t.Fatalf("advertised schema rejected shorthand: %v", err)
		}
	}
	raw, _ := json.Marshal(args)
	v, err := s.call(context.Background(), "send_message", raw)
	if err != nil {
		t.Fatal(err)
	}
	m := v.(comitia.Message)
	if m.Envelope == nil || !reflect.DeepEqual(m.Envelope.RecipientInstances, []string{"a", "z"}) || m.Envelope.RecipientSessionIDs[0] != ps[0].SessionID {
		t.Fatalf("MCP recipients: %+v", m)
	}
	args["dedupe_key"] = "invalid"
	args["recipient_instances"] = []any{"has space"}
	raw, _ = json.Marshal(args)
	if _, err = s.call(context.Background(), "send_message", raw); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("MCP validation: %v", err)
	}
}
