// The tool registry: tool groups in tools/list order, the advertised tool set
// and dispatch of a tool call to the group that serves it.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/krimvp/comitia/pkg/comitia"
)

func tool(name, description string, input map[string]any) map[string]any {
	return map[string]any{"name": name, "description": description, "inputSchema": input}
}

// toolGroups lists each domain's tools and handler, in tools/list order.
var toolGroups = []struct {
	tools func() []map[string]any
	call  func(*Server, context.Context, string, json.RawMessage) (any, error)
}{
	{serverInfoTools, (*Server).callServerInfo},
	{messageTools, (*Server).callMessages},
	{taskTools, (*Server).callTasks},
	{contextTools, (*Server).callContexts},
	{grantTools, (*Server).callGrants},
	{sessionTools, (*Server).callSessions},
	{presenceTools, (*Server).callPresence},
}

// toolNames is the set of every tool name tools/list can advertise.
var toolNames = sync.OnceValue(func() map[string]bool {
	names := make(map[string]bool)
	for _, t := range tools() {
		names[t["name"].(string)] = true
	}
	return names
})

func tools() []map[string]any {
	var out []map[string]any
	for _, g := range toolGroups {
		out = append(out, g.tools()...)
	}
	for _, t := range out {
		props := t["inputSchema"].(map[string]any)["properties"].(map[string]any)
		props["compact"] = nullableProperty(map[string]any{"type": "boolean", "description": "Short text summary by default; full result remains in structuredContent. Set false for full JSON text."})
	}
	return out
}

// errUnknownTool lets a domain handler decline a tool name it does not serve.
var errUnknownTool = errors.New("unknown tool")

func (s *Server) call(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	for _, g := range toolGroups {
		if v, err := g.call(s, ctx, name, raw); err != errUnknownTool {
			return collectionResult(v), s.withUnknownFieldHint(ctx, err)
		}
	}
	return nil, comitia.Invalidf("unknown tool")
}
