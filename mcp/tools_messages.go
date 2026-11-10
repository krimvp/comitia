package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/krimvp/comitia"
)

func messageTools() []map[string]any {
	recipients := envelopeSchema()["properties"].(map[string]any)
	return []map[string]any{
		tool("get_message", "Read one message in a readable context; authenticated sender is SessionID, instances are self-reported.", schema(map[string]any{"context_id": str("Readable context ID"), "message_id": str("Message ID in this context")}, "context_id", "message_id")),
		tool("list_message_schemas", "Discover registered message schema IDs, payload JSON Schemas, task/body requirements and examples, including configured extensions.", schema(map[string]any{})),
		tool("get_message_schema", "Get one registered versioned message schema descriptor; use its example envelope with real context/task IDs.", schema(map[string]any{"schema": str("Exact registered versioned message schema ID")}, "schema")),
		tool("send_message", "Append a message; top-level recipients route plain notes without an envelope; session IDs, not names. Context readers can see all messages. Omitted agent_instance defaults to COMITIA_AGENT_INSTANCE / COMITIA_AGENT_INSTANCE_LABEL.", schema(map[string]any{"context_id": str("Context ID"), "dedupe_key": map[string]any{"type": "string", "maxLength": comitia.MaxDedupeKeyBytes, "description": "Stable caller-chosen key (at most 256 bytes); scoped to this session and instance. A conflict names the existing message ID and sequence"}, "body": limited("Message explanation; required for untyped/discussion messages", comitia.MaxMessageBodyBytes), "recipient_session_ids": recipients["recipient_session_ids"], "recipient_instances": recipients["recipient_instances"], "envelope": envelopeSchema(), "agent_instance": agentInstanceSchema(), "task_fence": schema(map[string]any{"task_id": str("Must match envelope.task_id"), "claim_token": str("Current opaque lease token; never published"), "lease_version": integer("Current lease version; requires a live claim owned by this session")}, "task_id", "claim_token", "lease_version")}, "context_id", "dedupe_key")),
		tool("list_participants", "Discover active readable sessions in a context. Names may repeat; address by SessionID.", schema(map[string]any{"context_id": str("Context ID"), "limit": integer("Maximum participants (default 100)"), "page": map[string]any{"type": "boolean", "description": "Return participants, next_after and has_more"}, "after_id": str("Exclusive session ID from next_after; implies page")}, "context_id")),
		tool("replay_messages", "Read context messages after a sequence; renewed task events are skipped unless task-event schema is selected", schema(messageReadProperties(false), "context_id")),
		tool("ack_messages", "Record consumption through a sequence; omitted agent_instance defaults to COMITIA_AGENT_INSTANCE", schema(map[string]any{"context_id": str("Context ID"), "sequence": integer("Inclusive sequence"), "agent_instance": str("Optional instance ID; defaults to COMITIA_AGENT_INSTANCE; isolates this cursor within the authenticated session")}, "context_id", "sequence")),
		tool("message_cursor", "Read this session's persisted acknowledgement cursor; omitted agent_instance defaults to COMITIA_AGENT_INSTANCE", schema(map[string]any{"context_id": str("Context ID"), "agent_instance": str("Optional instance ID used by ack_messages; defaults to COMITIA_AGENT_INSTANCE")}, "context_id")),
		tool("wait_messages", "Wait for matching context messages (default 30 seconds, capped at 60); returns up to 100 or empty on timeout", schema(messageReadProperties(true), "context_id")),
	}
}

func messageReadProperties(wait bool) map[string]any {
	p := map[string]any{"context_id": str("Context ID"), "after": integer("Exclusive sequence cursor"), "for_instance": str("Recipient instance, caller session with no instance recipients, or broadcast"), "from_instance": str("Self-reported sender instance"), "task_id": str("Envelope task ID"), "schema": str("Exact envelope schema ID"), "agent_instance": str("Optional self-reported instance ID for presence; defaults to COMITIA_AGENT_INSTANCE")}
	p["exclude_self"] = map[string]any{"type": "boolean", "description": "Omit messages from this authenticated session and agent_instance (empty instance selects legacy sender)"}
	p["from_cursor"] = map[string]any{"type": "boolean", "description": "Start at this caller's agent_instance acknowledgement cursor; omit after"}
	p["schemas"] = map[string]any{"type": "array", "items": map[string]any{"type": []string{"string", "null"}}, "description": "Match any listed schema; selecting comitia.task-event.v1 includes renewed events"}
	p["reply_to"] = str("Direct replies to a message in this context")
	p["thread_id"] = str("Root message and recursive descendants in this context")
	p["from_session"] = str("Authenticated sender session ID")
	p["strict_recipients"] = map[string]any{"type": "boolean", "description": "Require both nonempty recipient lists to match caller session and for_instance; requires for_instance; default false preserves instance precedence"}
	p["max_bytes"] = integer("Encoded messages-array byte budget (default 1048576, max 4194304); first message always returned intact")
	p["page"] = map[string]any{"type": "boolean", "description": "Return messages, next_after, scanned_through and has_more instead of the legacy array"}
	p["compact"] = map[string]any{"type": "boolean", "description": "Short text summary; full result remains in structuredContent"}
	if wait {
		p["timeout_seconds"] = map[string]any{"type": "number", "description": "Timeout seconds (default 30, capped at 60; zero reads immediately)"}
	} else {
		p["limit"] = integer("Maximum matching messages")
	}
	return p
}

func (s *Server) callMessages(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "get_message":
		var a struct {
			ContextID string `json:"context_id"`
			MessageID string `json:"message_id"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("context_id", a.ContextID, "message_id", a.MessageID); err != nil {
			return nil, err
		}
		return comitia.LookupMessage(ctx, s.API, s.Credential, a.ContextID, a.MessageID)
	case "list_message_schemas", "get_message_schema":
		var a struct {
			Schema string `json:"schema"`
		}
		if name == "list_message_schemas" {
			if err := decode(raw, &struct{}{}); err != nil {
				return nil, err
			}
		} else {
			if err := decode(raw, &a); err != nil {
				return nil, err
			}
			if err := required("schema", a.Schema); err != nil {
				return nil, err
			}
		}
		api, ok := s.API.(comitia.MessageSchemaAPI)
		if !ok {
			return nil, comitia.Invalidf("message schema discovery unsupported by API")
		}
		if name == "list_message_schemas" {
			return api.ListMessageSchemas(ctx, s.Credential)
		}
		return api.GetMessageSchema(ctx, s.Credential, a.Schema)
	case "send_message":
		var a struct {
			RecipientSessionIDs []string                 `json:"recipient_session_ids"`
			RecipientInstances  []string                 `json:"recipient_instances"`
			TaskFence           *comitia.TaskFence       `json:"task_fence"`
			ContextID           string                   `json:"context_id"`
			DedupeKey           string                   `json:"dedupe_key"`
			Body                string                   `json:"body"`
			Envelope            *comitia.MessageEnvelope `json:"envelope"`
			AgentInstance       *comitia.AgentInstance   `json:"agent_instance"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("context_id", a.ContextID, "dedupe_key", a.DedupeKey); err != nil {
			return nil, err
		}
		return s.API.AppendMessage(ctx, s.Credential, a.ContextID, comitia.MessageInput{RecipientSessionIDs: a.RecipientSessionIDs, RecipientInstances: a.RecipientInstances, TaskFence: a.TaskFence, DedupeKey: a.DedupeKey, Body: a.Body, Envelope: a.Envelope, AgentInstance: s.InstanceDefaults.Message(a.AgentInstance)})
	case "list_participants":
		var a struct {
			Page      bool   `json:"page"`
			AfterID   string `json:"after_id"`
			ContextID string `json:"context_id"`
			Limit     *int   `json:"limit"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("context_id", a.ContextID); err != nil {
			return nil, err
		}
		limit := 100
		if a.Limit != nil {
			limit = *a.Limit
		}
		if limit < 1 || limit > 1000 {
			return nil, comitia.Invalidf("limit must be between 1 and 1000")
		}
		if a.Page || a.AfterID != "" {
			return comitia.ListParticipantPage(ctx, s.API, s.Credential, a.ContextID, a.AfterID, limit)
		}
		return s.API.ListParticipants(ctx, s.Credential, a.ContextID, limit)
	case "replay_messages":
		var a struct {
			comitia.MessageFilters
			Page      bool   `json:"page"`
			Compact   bool   `json:"compact"`
			ContextID string `json:"context_id"`
			After     *int64 `json:"after"`
			Limit     int    `json:"limit"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("context_id", a.ContextID); err != nil {
			return nil, err
		}
		if a.Limit == 0 {
			a.Limit = 100
		}
		filters, err := presenceFilters(s.InstanceDefaults, a.MessageFilters)
		if err != nil {
			return nil, err
		}
		if err := comitia.CheckMessageQueryAPI(s.API, filters); err != nil {
			return nil, err
		}
		after, err := messageAfter(a.After, filters)
		if err != nil {
			return nil, err
		}
		if a.Page || filters.FromCursor {
			page, err := comitia.ReplayMessagePage(ctx, s.API, s.Credential, a.ContextID, after, a.Limit, filters)
			if !a.Page {
				return cursorMessageResult(page), err
			}
			return messageResult(page, a.Compact), err
		}
		value, err := s.API.ReplayMessages(ctx, s.Credential, a.ContextID, after, a.Limit, filters)
		return messageResult(value, a.Compact), err
	case "wait_messages":
		var a struct {
			comitia.MessageFilters
			Page           bool     `json:"page"`
			Compact        bool     `json:"compact"`
			ContextID      string   `json:"context_id"`
			After          *int64   `json:"after"`
			TimeoutSeconds *float64 `json:"timeout_seconds"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("context_id", a.ContextID); err != nil {
			return nil, err
		}
		timeout := 30 * time.Second
		if a.TimeoutSeconds != nil {
			var err error
			timeout, err = comitia.MessageWaitTimeout(*a.TimeoutSeconds)
			if err != nil {
				return nil, err
			}
		}
		filters, err := presenceFilters(s.InstanceDefaults, a.MessageFilters)
		if err != nil {
			return nil, err
		}
		if err := comitia.CheckMessageQueryAPI(s.API, filters); err != nil {
			return nil, err
		}
		after, err := messageAfter(a.After, filters)
		if err != nil {
			return nil, err
		}
		if a.Page || filters.FromCursor {
			page, err := comitia.WaitMessagePage(ctx, s.API, s.Credential, a.ContextID, after, timeout, filters)
			if !a.Page {
				return cursorMessageResult(page), err
			}
			return messageResult(page, a.Compact), err
		}
		value, err := s.API.WaitMessages(ctx, s.Credential, a.ContextID, after, timeout, filters)
		return messageResult(value, a.Compact), err
	case "ack_messages":
		var a struct {
			ContextID     string  `json:"context_id"`
			Sequence      *int64  `json:"sequence"`
			AgentInstance *string `json:"agent_instance"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("context_id", a.ContextID); err != nil {
			return nil, err
		}
		if a.Sequence == nil {
			return nil, comitia.Invalidf("sequence required")
		}
		instance, err := instanceID(s.InstanceDefaults, a.AgentInstance)
		if err != nil {
			return nil, err
		}
		if err := s.API.AckMessages(ctx, s.Credential, a.ContextID, *a.Sequence, instance); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	case "message_cursor":
		var a struct {
			AgentInstance *string `json:"agent_instance"`
			ContextID     string  `json:"context_id"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("context_id", a.ContextID); err != nil {
			return nil, err
		}
		instance, err := instanceID(s.InstanceDefaults, a.AgentInstance)
		if err != nil {
			return nil, err
		}
		v, err := s.API.MessageCursor(ctx, s.Credential, a.ContextID, instance)
		if err != nil {
			return nil, err
		}
		return map[string]int64{"sequence": v}, nil
	default:
		return nil, errUnknownTool
	}
}

type compactResult struct {
	value any
	text  string
}

func messageResult(value any, compact bool) any {
	if !compact {
		return value
	}
	var count int
	var after int64
	var text string
	switch v := value.(type) {
	case comitia.MessagePage:
		count, after = len(v.Messages), v.NextAfter
		text = fmt.Sprintf("%d messages; next_after=%d; has_more=%t. Full result in structuredContent.", count, after, v.HasMore)
	case []comitia.Message:
		count = len(v)
		if count > 0 {
			after = v[count-1].Sequence
		}
		text = fmt.Sprintf("%d messages; next_after=%d; last_sequence=%d. Full result in structuredContent.", count, after, after)
	}
	return compactResult{value: value, text: text}
}

func messageAfter(after *int64, f comitia.MessageFilters) (int64, error) {
	if after != nil {
		if f.FromCursor {
			return 0, comitia.Invalidf("from_cursor cannot be combined with after")
		}
		return *after, nil
	}
	return 0, nil
}

func cursorMessageResult(page comitia.MessagePage) any {
	return compactResult{value: page.Messages, text: fmt.Sprintf("%d messages; next_after=%d; scanned_through=%d; has_more=%t. Full result in structuredContent.", len(page.Messages), page.NextAfter, page.ScannedThrough, page.HasMore)}
}
