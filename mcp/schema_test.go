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
