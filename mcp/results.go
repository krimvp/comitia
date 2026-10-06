package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/krimvp/comitia"
)

// Compact is an MCP presentation option, never forwarded to the backing API.
func resultArguments(raw json.RawMessage) (json.RawMessage, bool, error) {
	var a struct {
		Compact *bool `json:"compact"`
	}
	if len(raw) == 0 || !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return raw, true, nil // The tool decoder validates object arguments.
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, true, comitia.InvalidJSON(err, "arguments")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, true, comitia.InvalidJSON(err, "arguments")
	}
	if _, ok := fields["compact"]; ok {
		delete(fields, "compact")
		raw, _ = json.Marshal(fields)
	}
	return raw, a.Compact == nil || *a.Compact, nil
}

func resultSummary(name string, value any, arguments json.RawMessage, size int) string {
	const suffix = ". Full result in structuredContent."
	switch v := value.(type) {
	case comitia.MessagePage:
		return fmt.Sprintf("%d messages; next_after=%d; scanned_through=%d; has_more=%t%s", len(v.Messages), v.NextAfter, v.ScannedThrough, v.HasMore, suffix)
	case []comitia.Message:
		var a struct {
			After int64 `json:"after"`
		}
		_ = json.Unmarshal(arguments, &a)
		if len(v) > 0 {
			a.After = v[len(v)-1].Sequence
		}
		return fmt.Sprintf("%d messages; next_after=%d; last_sequence=%d%s", len(v), a.After, a.After, suffix)
	case comitia.Message:
		return fmt.Sprintf("message id=%s; sequence=%d%s", v.ID, v.Sequence, suffix)
	case comitia.TaskPage:
		return fmt.Sprintf("%d tasks; next_after=%s; has_more=%t%s", len(v.Tasks), v.NextAfter, v.HasMore, suffix)
	case comitia.ParticipantPage:
		return fmt.Sprintf("%d participants; next_after=%s; has_more=%t%s", len(v.Participants), v.NextAfter, v.HasMore, suffix)
	case comitia.Task:
		return fmt.Sprintf("task id=%s; status=%s; lease_version=%d%s", v.ID, v.Status, v.LeaseVersion, suffix)
	case comitia.TaskClaim:
		return fmt.Sprintf("task id=%s; status=%s; lease_version=%d%s", v.Task.ID, v.Task.Status, v.Task.LeaseVersion, suffix)
	case comitia.MessageSchemaDescriptor:
		return fmt.Sprintf("message schema id=%s%s", v.ID, suffix)
	}
	v := reflect.ValueOf(value)
	if v.IsValid() && v.Kind() == reflect.Slice {
		return fmt.Sprintf("%s: %d items%s", name, v.Len(), suffix)
	}
	if size > 512 {
		return name + " succeeded" + suffix
	}
	return ""
}
