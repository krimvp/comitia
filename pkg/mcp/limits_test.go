package mcp

import (
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

func TestContentLimitSchemas(t *testing.T) {
	want := map[string]map[string]int{
		"send_message":  {"body": comitia.MaxMessageBodyBytes},
		"create_task":   {"title": comitia.MaxTaskTitleBytes, "body": comitia.MaxTaskBodyBytes},
		"add_reference": {"kind": comitia.MaxReferenceKindBytes, "uri": comitia.MaxReferenceURIBytes},
	}
	for _, tool := range tools() {
		fields, ok := want[tool["name"].(string)]
		if !ok {
			continue
		}
		delete(want, tool["name"].(string))
		props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		for field, limit := range fields {
			if got := props[field].(map[string]any)["maxLength"]; got != limit {
				t.Errorf("%s.%s maxLength=%v, want %d", tool["name"], field, got, limit)
			}
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing tools: %v", want)
	}
}
