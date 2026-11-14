// JSON-schema builders for tool inputs.

package mcp

import (
	"fmt"

	"github.com/krimvp/comitia/pkg/comitia"
)

func schema(props map[string]any, required ...string) map[string]any {
	mandatory := make(map[string]bool, len(required))
	for _, name := range required {
		mandatory[name] = true
	}
	properties := make(map[string]any, len(props))
	for name, property := range props {
		if !mandatory[name] {
			property = nullableProperty(property.(map[string]any))
		}
		properties[name] = property
	}
	s := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

// Optional Go decoder fields accept null as their omitted/default value.
func nullableProperty(property map[string]any) map[string]any {
	typ, simple := property["type"].(string)
	if _, enum := property["enum"]; enum || !simple {
		return map[string]any{"anyOf": []any{property, map[string]any{"type": "null"}}}
	}
	out := make(map[string]any, len(property))
	for key, value := range property {
		out[key] = value
	}
	out["type"] = []string{typ, "null"}
	return out
}
func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

// limited advertises a byte limit; maxLength counts characters, so it is the
// same number as a looser bound and the description states the byte rule.
func limited(desc string, bytes int) map[string]any {
	return map[string]any{"type": "string", "maxLength": bytes, "description": fmt.Sprintf("%s (at most %d bytes of UTF-8)", desc, bytes)}
}
func integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

// listLimitDescription is the limit description of a list tool.
func listLimitDescription(items string) string {
	return fmt.Sprintf("Maximum %s, 1-%d (default %d)", items, comitia.MaxListLimit, comitia.DefaultListLimit)
}
