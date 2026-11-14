package mcp

import (
	"encoding/json"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestAdvertisedSchemasStrictClient(t *testing.T) {
	responses := serve(t, &fakeAPI{}, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"+`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")
	listed := responses[1]["result"].(map[string]any)["tools"].([]any)
	if len(listed) != len(tools()) {
		t.Fatalf("listed %d tools, want %d", len(listed), len(tools()))
	}
	for _, draft := range []*jsonschema.Draft{jsonschema.Draft7, jsonschema.Draft2020} {
		t.Run(draft.String(), func(t *testing.T) {
			compiler := jsonschema.NewCompiler()
			compiler.DefaultDraft(draft)
			for _, advertised := range listed {
				tool := advertised.(map[string]any)
				t.Run(tool["name"].(string), func(t *testing.T) {
					url := "https://comitia.test/" + tool["name"].(string)
					if err := compiler.AddResource(url, tool["inputSchema"]); err != nil {
						t.Fatal(err)
					}
					// Compilation validates against the draft's metaschema, as a strict client does.
					compiled, err := compiler.Compile(url)
					if err != nil {
						t.Fatal(err)
					}
					if tool["name"] == "server_info" || tool["name"] == "list_contexts" {
						if err := compiled.Validate(map[string]any{}); err != nil {
							t.Fatalf("no required arguments: %v", err)
						}
					}
				})
			}
		})
	}
}

func TestSchemaRequiredEncoding(t *testing.T) {
	for _, fields := range [][]string{nil, {}, {"id"}} {
		b, err := json.Marshal(schema(map[string]any{"id": str("ID")}, fields...))
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatal(err)
		}
		if value, present := got["required"]; present {
			array, ok := value.([]any)
			if !ok || len(array) != len(fields) {
				t.Fatalf("required must be an array: %s", b)
			}
		} else if len(fields) != 0 {
			t.Fatalf("required fields lost: %s", b)
		}
	}
}

func TestMessageCapabilitiesAdvertised(t *testing.T) {
	responses := serve(t, &fakeAPI{}, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"+`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")
	advertised := map[string]map[string]any{}
	for _, value := range responses[1]["result"].(map[string]any)["tools"].([]any) {
		tool := value.(map[string]any)
		advertised[tool["name"].(string)] = tool["inputSchema"].(map[string]any)
	}
	for _, name := range []string{"list_message_schemas", "get_message_schema", "replay_messages", "wait_messages"} {
		t.Run(name, func(t *testing.T) {
			input, ok := advertised[name]
			if !ok {
				t.Fatalf("tools/list missing %s", name)
			}
			compiler := jsonschema.NewCompiler()
			url := "https://comitia.test/advertised/" + name
			if err := compiler.AddResource(url, input); err != nil {
				t.Fatal(err)
			}
			compiled, err := compiler.Compile(url)
			if err != nil {
				t.Fatal(err)
			}
			valid := map[string]any{}
			switch name {
			case "get_message_schema":
				valid["schema"] = "comitia.review-request.v1"
			case "replay_messages", "wait_messages":
				property, ok := input["properties"].(map[string]any)["strict_recipients"].(map[string]any)
				// Optional properties are nullable because the handler treats null as omitted.
				if typ, _ := json.Marshal(property["type"]); !ok || string(typ) != `["boolean","null"]` {
					t.Fatal("strict_recipients must be advertised as boolean")
				}
				valid = map[string]any{"context_id": "context", "for_instance": "worker", "strict_recipients": true}
			}
			if err := compiled.Validate(valid); err != nil {
				t.Fatalf("valid arguments rejected: %v", err)
			}
			invalid := map[string]any{}
			for key, value := range valid {
				invalid[key] = value
			}
			switch name {
			case "list_message_schemas":
				invalid["schema"] = "unexpected"
			case "get_message_schema":
				delete(invalid, "schema")
			default:
				invalid["strict_recipients"] = "true"
			}
			if err := compiled.Validate(invalid); err == nil {
				t.Fatalf("invalid arguments accepted: %v", invalid)
			}
		})
	}
}
