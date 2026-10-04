package mcp

import (
	"context"
	"encoding/json"
)

func serverInfoTools() []map[string]any {
	return []map[string]any{tool("server_info", "Server version, protocol version and supported features; requires a valid session", schema(map[string]any{}))}
}

func (s *Server) callServerInfo(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	if name != "server_info" {
		return nil, errUnknownTool
	}
	if err := decode(raw, &struct{}{}); err != nil {
		return nil, err
	}
	return s.API.ServerInfo(ctx, s.Credential)
}
