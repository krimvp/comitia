// JSON-RPC framing and dispatch shared by both transports: message types,
// request decoding and handle (initialize, ping, server/discover, tools/list,
// tools/call and tool error mapping).

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/krimvp/comitia"
)

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	// Result and Error mark a JSON-RPC response sent by the client.
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// readRequest decodes one JSON-RPC message for either transport. A non-nil
// reject is the reply. With neither a reject nor a method, the message is a
// response from the client and needs no answer.
func readRequest(b []byte) (request, *response) {
	var req request
	reject := func(id json.RawMessage, code int, message string) (request, *response) {
		if stdioRequestKey(id) == "" {
			id = nil // null, structured or fractional ids are never echoed
		}
		return req, &response{JSONRPC: "2.0", ID: id, Error: &rpcError{code, message}}
	}
	if err := json.Unmarshal(b, &req); err != nil {
		if !json.Valid(b) {
			return reject(nil, -32700, "parse error")
		}
		if trimmed := bytes.TrimSpace(b); trimmed[0] == '[' {
			// MCP 2025-06-18 removed JSON-RPC batching.
			return reject(nil, -32600, "batch requests are not supported")
		}
		return reject(nil, -32600, "invalid request")
	}
	if req.Method == "" && (req.Result != nil || req.Error != nil) {
		return req, nil
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		return reject(req.ID, -32600, "invalid request")
	}
	// MCP ids are strings or integers; null is not a request id.
	if len(req.ID) > 0 && stdioRequestKey(req.ID) == "" {
		return reject(nil, -32600, "invalid request: id must be a string or integer")
	}
	return req, nil
}

func stdioRequestKey(id json.RawMessage) string {
	var value any
	dec := json.NewDecoder(bytes.NewReader(id))
	dec.UseNumber()
	if dec.Decode(&value) != nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return "string:" + v
	case json.Number:
		if strings.ContainsAny(string(v), ".eE") {
			return ""
		}
		if v == "-0" {
			v = "0"
		}
		return "number:" + string(v)
	default:
		return ""
	}
}

func (s *Server) handle(ctx context.Context, req request) response {
	r := response{JSONRPC: "2.0", ID: req.ID}
	if req.JSONRPC != "2.0" {
		r.Error = &rpcError{-32600, "invalid request"}
		return r
	}
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(req.Params, &p) != nil || p.ProtocolVersion == "" {
			r.Error = &rpcError{-32602, "invalid params"}
			return r
		}
		r.Result = map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "comitia", "version": comitia.BuildVersion(), "build": comitia.BuildIdentity()}}
	case "ping":
		r.Result = map[string]any{}
	case "server/discover":
		// Modern (2026-07-28) era negotiation. Clients that speak this revision
		// probe here before the initialize handshake. Comitia coordinates work
		// through tools only, so it advertises no resources, prompts,
		// completions or logging capability - doing so would invite clients to
		// call methods this server does not implement.
		r.Result = map[string]any{
			"supportedVersions": ProtocolVersions,
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"serverInfo":        map[string]any{"name": "comitia", "version": comitia.BuildVersion(), "build": comitia.BuildIdentity()},
		}
	case "tools/list":
		r.Result = map[string]any{"tools": tools()}
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if json.Unmarshal(req.Params, &p) != nil || p.Name == "" {
			r.Error = &rpcError{-32602, "invalid params"}
			return r
		}
		if !toolNames()[p.Name] {
			// MCP reports an unknown tool as a protocol error, not a tool result.
			r.Error = &rpcError{-32602, "unknown tool"}
			return r
		}
		started := time.Now()
		requestID := comitia.NewRequestID()
		arguments, compact, err := resultArguments(p.Arguments)
		var v any
		if err == nil {
			v, err = s.call(ctx, p.Name, arguments)
		}
		if err != nil {
			operation := comitia.SafeOperation(p.Name)
			info := comitia.SafeError(operation, err, requestID)
			comitia.ReportFailure(s.DiagnosticHook, operation, err, info.RequestID, time.Since(started))
			if orphan := (orphanedSessionError{}); errors.As(err, &orphan) {
				info.Message = orphan.Error()
			}
			r.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": info.String()}}, "structuredContent": map[string]any{"error": info}, "isError": true}
			return r
		}
		var summary string
		if compact, ok := v.(compactResult); ok {
			v, summary = compact.value, compact.text
		}
		wrapped := map[string]any{"result": v}
		b, err := json.Marshal(wrapped)
		if err != nil {
			r.Error = &rpcError{-32603, "internal error"}
			return r
		}
		if compact && summary == "" {
			summary = resultSummary(p.Name, v, p.Arguments, len(b))
		}
		if compact && len(summary) > 512 {
			summary = p.Name + " succeeded. Full result in structuredContent."
		}
		if !compact || summary == "" {
			summary = string(b)
		}
		r.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": summary}}, "structuredContent": wrapped, "isError": false}
	default:
		r.Error = &rpcError{-32601, "method not found"}
	}
	return r
}
