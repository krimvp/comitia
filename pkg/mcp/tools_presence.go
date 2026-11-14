// Presence tools: list_instances and set_instance_resume.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/krimvp/comitia/pkg/comitia"
)

func agentInstanceSchema() map[string]any {
	return schema(map[string]any{"id": str("Self-reported instance ID: 1-64 ASCII letters, digits, dot, underscore or hyphen; never identity proof"), "label": str("Optional self-reported display label (at most 128 bytes)")}, "id")
}

func presenceTools() []map[string]any {
	return []map[string]any{
		tool("list_instances", "List self-reported instance presence and resume metadata in a readable context; seen_seconds_ago uses the server clock, recent means seen within "+fmt.Sprint(comitia.InstanceRecentWindow.Seconds())+" seconds, active means the session is valid. Activity does not prove liveness.", schema(map[string]any{"context_id": str("Context ID"), "limit": integer(fmt.Sprintf("Maximum instances (default %d)", comitia.DefaultInstanceListLimit)), "page": map[string]any{"type": "boolean", "description": "Return instances, next_after and has_more"}, "after_id": str("Exclusive cursor from next_after; implies page")}, "context_id")),
		tool("set_instance_resume", "Set self-reported resume metadata for this session's instance. Never used for authorization. Omitted agent_instance defaults to COMITIA_AGENT_INSTANCE / COMITIA_AGENT_INSTANCE_LABEL.", schema(map[string]any{"context_id": str("Context ID"), "agent_instance": agentInstanceSchema(), "resume": map[string]any{"type": "object", "description": fmt.Sprintf("Self-reported resume metadata, at most %d JSON bytes (e.g. harness, session_id, cwd, pid)", comitia.MaxInstanceResumeBytes), "additionalProperties": true}}, "context_id", "resume")),
	}
}

func (s *Server) callPresence(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "list_instances":
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
		if !a.Page && a.AfterID == "" && a.Limit == nil {
			return s.API.ListInstances(ctx, s.Credential, a.ContextID)
		}
		limit := comitia.DefaultInstanceListLimit
		if a.Limit != nil {
			limit = *a.Limit
		}
		page, err := comitia.ListInstancePage(ctx, s.API, s.Credential, a.ContextID, a.AfterID, limit)
		if err != nil || a.Page || a.AfterID != "" {
			return page, err
		}
		return page.Instances, nil
	case "set_instance_resume":
		var a struct {
			ContextID     string                 `json:"context_id"`
			AgentInstance *comitia.AgentInstance `json:"agent_instance"`
			Resume        json.RawMessage        `json:"resume"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("context_id", a.ContextID); err != nil {
			return nil, err
		}
		if err := s.API.SetInstanceResume(ctx, s.Credential, a.ContextID, s.InstanceDefaults.Message(a.AgentInstance), a.Resume); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	default:
		return nil, errUnknownTool
	}
}
