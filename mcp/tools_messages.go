package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/krimvp/comitia"
)

func messageTools() []map[string]any {
	return []map[string]any{
		tool("send_message", "Append a message; optional validated envelope addresses session IDs, not names. Context readers can see all messages. Omitted agent_instance defaults to COMITIA_AGENT_INSTANCE / COMITIA_AGENT_INSTANCE_LABEL.", schema(map[string]any{"context_id": str("Context ID"), "dedupe_key": str("Stable caller-chosen key; scoped to this session and instance"), "body": str("Message explanation; required for untyped/discussion messages"), "envelope": envelopeSchema(), "agent_instance": schema(map[string]any{"id": str("Self-reported instance ID: 1-64 ASCII letters, digits, dot, underscore or hyphen; never identity proof"), "label": str("Optional self-reported display label (at most 128 bytes)")}, "id")}, "context_id", "dedupe_key")),
		tool("list_participants", "Discover active readable sessions in a context. Names may repeat; address by SessionID.", schema(map[string]any{"context_id": str("Context ID"), "limit": integer("Maximum participants (default 100)")}, "context_id")),
		tool("replay_messages", "Read context messages after a sequence", schema(messageReadProperties(false), "context_id")),
		tool("ack_messages", "Record consumption through a sequence; omitted agent_instance defaults to COMITIA_AGENT_INSTANCE", schema(map[string]any{"context_id": str("Context ID"), "sequence": integer("Inclusive sequence"), "agent_instance": str("Optional instance ID; defaults to COMITIA_AGENT_INSTANCE; isolates this cursor within the authenticated session")}, "context_id", "sequence")),
		tool("message_cursor", "Read this session's persisted acknowledgement cursor; omitted agent_instance defaults to COMITIA_AGENT_INSTANCE", schema(map[string]any{"context_id": str("Context ID"), "agent_instance": str("Optional instance ID used by ack_messages; defaults to COMITIA_AGENT_INSTANCE")}, "context_id")),
		tool("wait_messages", "Wait for matching context messages (default 30 seconds, capped at 60); returns up to 100 or empty on timeout", schema(messageReadProperties(true), "context_id")),
	}
}

func messageReadProperties(wait bool) map[string]any {
	p := map[string]any{"context_id": str("Context ID"), "after": integer("Exclusive sequence cursor"), "for_instance": str("Recipient instance or broadcast with no recipients"), "from_instance": str("Self-reported sender instance"), "task_id": str("Envelope task ID"), "schema": str("Exact envelope schema ID")}
	if wait {
		p["timeout_seconds"] = map[string]any{"type": "number", "description": "Timeout seconds (default 30, capped at 60; zero reads immediately)"}
	} else {
		p["limit"] = integer("Maximum matching messages")
	}
	return p
}

func (s *Server) callMessages(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "send_message":
		var a struct {
			ContextID     string                   `json:"context_id"`
			DedupeKey     string                   `json:"dedupe_key"`
			Body          string                   `json:"body"`
			Envelope      *comitia.MessageEnvelope `json:"envelope"`
			AgentInstance *comitia.AgentInstance   `json:"agent_instance"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.ContextID, a.DedupeKey); err != nil {
			return nil, err
		}
		return s.API.AppendMessage(ctx, s.Credential, a.ContextID, comitia.MessageInput{DedupeKey: a.DedupeKey, Body: a.Body, Envelope: a.Envelope, AgentInstance: s.InstanceDefaults.Message(a.AgentInstance)})
	case "list_participants":
		var a struct {
			ContextID string `json:"context_id"`
			Limit     *int   `json:"limit"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.ContextID); err != nil {
			return nil, err
		}
		limit := 100
		if a.Limit != nil {
			limit = *a.Limit
		}
		if limit < 1 || limit > 1000 {
			return nil, comitia.ErrInvalid
		}
		return s.API.ListParticipants(ctx, s.Credential, a.ContextID, limit)
	case "replay_messages", "wait_messages":
		var a struct {
			comitia.MessageFilters
			ContextID      string   `json:"context_id"`
			After          int64    `json:"after"`
			Limit          int      `json:"limit"`
			TimeoutSeconds *float64 `json:"timeout_seconds"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.ContextID); err != nil {
			return nil, err
		}
		if a.Limit == 0 {
			a.Limit = 100
		}
		if name == "wait_messages" {
			timeout := 30 * time.Second
			if a.TimeoutSeconds != nil {
				var err error
				timeout, err = comitia.MessageWaitTimeout(*a.TimeoutSeconds)
				if err != nil {
					return nil, err
				}
			}
			return s.API.WaitMessages(ctx, s.Credential, a.ContextID, a.After, timeout, a.MessageFilters)
		}
		return s.API.ReplayMessages(ctx, s.Credential, a.ContextID, a.After, a.Limit, a.MessageFilters)
	case "ack_messages":
		var a struct {
			ContextID     string  `json:"context_id"`
			Sequence      int64   `json:"sequence"`
			AgentInstance *string `json:"agent_instance"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.ContextID); err != nil {
			return nil, err
		}
		instance, err := instanceID(s.InstanceDefaults, a.AgentInstance)
		if err != nil {
			return nil, err
		}
		if err := s.API.AckMessages(ctx, s.Credential, a.ContextID, a.Sequence, instance); err != nil {
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
		if err := required(a.ContextID); err != nil {
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
