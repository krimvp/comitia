package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/krimvp/comitia"
)

func TestSendMessageDedupeKeySchemaCap(t *testing.T) {
	var key map[string]any
	for _, tool := range tools() {
		if tool["name"] == "send_message" {
			key = tool["inputSchema"].(map[string]any)["properties"].(map[string]any)["dedupe_key"].(map[string]any)
		}
	}
	if key["maxLength"] != comitia.MaxDedupeKeyBytes || !strings.Contains(key["description"].(string), "256 bytes") {
		t.Fatalf("dedupe_key schema: %v", key)
	}
	s, _, _, c := contextServer(t, false)
	args, _ := json.Marshal(map[string]any{"context_id": c.ID, "dedupe_key": strings.Repeat("k", comitia.MaxDedupeKeyBytes+1), "body": "x"})
	if _, err := s.call(context.Background(), "send_message", args); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("long key: %v", err)
	}
}
