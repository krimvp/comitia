// Package mcp exposes a deliberately small Model Context Protocol stdio server.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/krimvp/comitia"
)

const ProtocolVersion = "2025-06-18"

// SupportedProtocolVersions lists the MCP revisions this adapter accepts. It is
// deliberately just the established revision: the 2026-07-28 "modern" era adds
// required cache/result metadata to every result envelope (cacheScope, ttlMs,
// resultType) and a per-request _meta handshake. Comitia's adapter is a plain
// JSON-RPC tool server, so it advertises the revision it actually implements
// rather than claiming a framing whose extra obligations it does not meet.
// Clients that prefer the modern era fall back cleanly when they see this.
var SupportedProtocolVersions = []string{ProtocolVersion}

// ProtocolVersions is the JSON field form of SupportedProtocolVersions, used in
// the server/discover result and in rejections.
var ProtocolVersions = SupportedProtocolVersions

// SupportedProtocolVersion reports whether rev is accepted.
func SupportedProtocolVersion(rev string) bool {
	for _, v := range SupportedProtocolVersions {
		if v == rev {
			return true
		}
	}
	return false
}

// Server binds one already-scoped credential to an MCP connection. Credentials
// are process configuration and are never accepted as tool arguments.
type Server struct {
	API              comitia.API
	Credential       string
	InstanceDefaults comitia.InstanceDefaults
	SessionsDir      string
	ServerURL        string
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
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

// Serve reads and writes newline-delimited JSON-RPC messages as required by the
// MCP stdio transport. Notifications do not receive responses.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if s.API == nil || s.Credential == "" {
		return errors.New("mcp: API and credential are required")
	}
	scan := bufio.NewScanner(in)
	// Tool results can contain sizeable message bodies.
	scan.Buffer(make([]byte, 64*1024), 4*1024*1024)
	enc := json.NewEncoder(out)
	initialized := false
	for scan.Scan() {
		var req request
		if err := json.Unmarshal(scan.Bytes(), &req); err != nil {
			if err := enc.Encode(response{JSONRPC: "2.0", Error: &rpcError{-32700, "parse error"}}); err != nil {
				return err
			}
			continue
		}
		if req.JSONRPC != "2.0" || req.Method == "" {
			if err := enc.Encode(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{-32600, "invalid request"}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue
		}
		if req.Method == "initialize" && initialized {
			if err := enc.Encode(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{-32600, "already initialized"}}); err != nil {
				return err
			}
			continue
		}
		if req.Method != "initialize" && req.Method != "ping" && !initialized {
			if err := enc.Encode(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{-32002, "server not initialized"}}); err != nil {
				return err
			}
			continue
		}
		res := s.handle(ctx, req)
		if req.Method == "initialize" && res.Error == nil {
			initialized = true
		}
		if err := enc.Encode(res); err != nil {
			return err
		}
	}
	return scan.Err()
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
		r.Result = map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "comitia", "version": "0.1.0"}}
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
			"serverInfo":        map[string]string{"name": "comitia", "version": "0.1.0"},
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
		v, err := s.call(ctx, p.Name, p.Arguments)
		if err != nil {
			err = publicError(err)
			r.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": err.Error()}}, "isError": true}
			return r
		}
		wrapped := map[string]any{"result": v}
		b, _ := json.Marshal(wrapped)
		r.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}, "structuredContent": wrapped, "isError": false}
	default:
		r.Error = &rpcError{-32601, "method not found"}
	}
	return r
}

func schema(props map[string]any, required ...string) map[string]any {
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}
func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func integer(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func tool(name, description string, input map[string]any) map[string]any {
	return map[string]any{"name": name, "description": description, "inputSchema": input}
}

// toolGroups lists each domain's tools and handler, in tools/list order.
var toolGroups = []struct {
	tools func() []map[string]any
	call  func(*Server, context.Context, string, json.RawMessage) (any, error)
}{
	{messageTools, (*Server).callMessages},
	{taskTools, (*Server).callTasks},
	{contextTools, (*Server).callContexts},
	{grantTools, (*Server).callGrants},
	{sessionTools, (*Server).callSessions},
}

func tools() []map[string]any {
	var out []map[string]any
	for _, g := range toolGroups {
		out = append(out, g.tools()...)
	}
	return out
}

func decode(raw json.RawMessage, dst any) error {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("%w: invalid arguments", comitia.ErrInvalid)
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return fmt.Errorf("%w: invalid arguments", comitia.ErrInvalid)
	}
	return nil
}
func required(values ...string) error {
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			return comitia.ErrInvalid
		}
	}
	return nil
}
func publicError(err error) error {
	for _, e := range []error{comitia.ErrNotFound, comitia.ErrUnauthenticated, comitia.ErrForbidden, comitia.ErrConflict, comitia.ErrInvalid, comitia.ErrStaleClaim, comitia.ErrBootstrapUsed} {
		if errors.Is(err, e) {
			return e
		}
	}
	return errors.New("internal error")
}
func seconds(n int64, fallback int64) (time.Duration, error) {
	if n == 0 {
		n = fallback
	}
	if n < 1 || n > 86400 {
		return 0, comitia.ErrInvalid
	}
	return time.Duration(n) * time.Second, nil
}

// errUnknownTool lets a domain handler decline a tool name it does not serve.
var errUnknownTool = errors.New("unknown tool")

func (s *Server) call(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	for _, g := range toolGroups {
		if v, err := g.call(s, ctx, name, raw); err != errUnknownTool {
			return v, err
		}
	}
	return nil, comitia.ErrInvalid
}
