package mcp

import (
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

// TestEveryToolHasOperationName keeps diagnostics and operation-specific
// hints from labelling a tool "unknown".
func TestEveryToolHasOperationName(t *testing.T) {
	for _, tool := range tools() {
		name := tool["name"].(string)
		if operation := comitia.SafeOperation(name); operation == "unknown" {
			t.Errorf("tool %q has no operation name", name)
		}
	}
}
