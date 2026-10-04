package mcp

import (
	"context"
	"encoding/json"

	"github.com/krimvp/comitia"
)

func agentInstanceSchema() map[string]any {
	return schema(map[string]any{"id": str("Self-reported instance ID: 1-64 ASCII letters, digits, dot, underscore or hyphen; never identity proof"), "label": str("Optional self-reported display label (at most 128 bytes)")}, "id")
}

// Shared by send and resume so configured defaults can be resolved in one place.
func (s *Server) resolveAgentInstance(instance *comitia.AgentInstance) *comitia.AgentInstance {
	return instance
}

func presenceTools() []map[string]any {
	return []map[string]any{
		tool("list_instances", "List self-reported instance presence and resume metadata in a readable context; last seen does not prove liveness.", schema(map[string]any{"context_id": str("Context ID")}, "context_id")),
		tool("set_instance_resume", "Set self-reported resume metadata for this session's instance. Never used for authorization.", schema(map[string]any{"context_id": str("Context ID"), "agent_instance": agentInstanceSchema(), "resume": map[string]any{"type": "object", "description": "Self-reported resume metadata, at most 4096 JSON bytes (e.g. harness, session_id, cwd, pid)", "additionalProperties": true}}, "context_id", "agent_instance", "resume")),
	}
}

func (s *Server) callPresence(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "list_instances":
		var a struct {
			ContextID string `json:"context_id"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("context_id", a.ContextID); err != nil {
			return nil, err
		}
		return s.API.ListInstances(ctx, s.Credential, a.ContextID)
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
		if err := s.API.SetInstanceResume(ctx, s.Credential, a.ContextID, s.resolveAgentInstance(a.AgentInstance), a.Resume); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	default:
		return nil, errUnknownTool
	}
}
