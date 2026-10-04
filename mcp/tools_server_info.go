package mcp

import (
	"context"
	"encoding/json"

	"github.com/krimvp/comitia"
)

func serverInfoTools() []map[string]any {
	return []map[string]any{tool("server_info", "Server version, protocol version and supported features; requires a valid session", schema(map[string]any{})), tool("storage_status", "Bounded writer readiness probe and process-local write failure/latency counters; requires an administrator", schema(map[string]any{}))}
}

func (s *Server) callServerInfo(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	if name != "server_info" && name != "storage_status" {
		return nil, errUnknownTool
	}
	if err := decode(raw, &struct{}{}); err != nil {
		return nil, err
	}
	if name == "storage_status" {
		api, ok := s.API.(comitia.StorageStatusAPI)
		if !ok {
			return nil, comitia.ErrNotFound
		}
		return api.StorageStatus(ctx, s.Credential)
	}
	return s.API.ServerInfo(ctx, s.Credential)
}
