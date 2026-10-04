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
	"log"
	"strings"
	"time"

	"github.com/krimvp/comitia"
)

const ProtocolVersion = comitia.ProtocolVersion

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
	DiagnosticHook   comitia.DiagnosticHook
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

const maxStdioCalls = 8
const stdioShutdownTimeout = time.Second

// Serve reads and writes newline-delimited JSON-RPC messages as required by the
// MCP stdio transport. Tool responses may arrive in any order, matched by ID.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if s.API == nil || s.Credential == "" {
		return errors.New("mcp: API and credential are required")
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	type input struct {
		line []byte
		err  error
	}
	scanLines := make(chan input)
	lines := scanLines
	go func() {
		scan := bufio.NewScanner(in)
		scan.Buffer(make([]byte, 64*1024), 4*1024*1024)
		for scan.Scan() {
			select {
			case scanLines <- input{line: bytes.Clone(scan.Bytes())}:
			case <-runCtx.Done():
				return
			}
		}
		select {
		case scanLines <- input{err: scan.Err()}:
		case <-runCtx.Done():
		}
	}()
	defer func() {
		// A closable reader lets shutdown interrupt an outstanding Scan.
		if lines != nil {
			if closer, ok := in.(io.Closer); ok {
				_ = closer.Close()
			}
		}
	}()
	type call struct {
		ctx    context.Context
		cancel context.CancelFunc
	}
	type completion struct {
		key string
		res response
	}
	pending := make(map[string]call)
	completed := make(chan completion, maxStdioCalls)
	defer func() {
		for _, c := range pending {
			c.cancel()
		}
	}()
	// Only this loop writes responses or changes connection state.
	enc := json.NewEncoder(out)
	initialized := false
	parentDone := ctx.Done()
	var shutdown <-chan time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	startShutdown := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.NewTimer(stdioShutdownTimeout)
		shutdown = timer.C
	}
	var serveErr error
	for {
		if lines == nil && len(pending) == 0 {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return serveErr
		}
		select {
		case <-parentDone:
			serveErr = ctx.Err()
			parentDone = nil
			lines = nil
			if closer, ok := in.(io.Closer); ok {
				_ = closer.Close()
			}
			stop()
			startShutdown()
		case <-shutdown:
			if runCtx.Err() != nil {
				return errors.Join(serveErr, errors.New("mcp: tool calls did not stop before shutdown deadline"))
			}
			cancelled := 0
			for _, c := range pending {
				if c.ctx.Err() == nil {
					cancelled++
				}
			}
			if cancelled > 0 {
				serveErr = fmt.Errorf("mcp: EOF shutdown cancelled %d tool calls", cancelled)
			}
			stop()
			startShutdown()
		case done := <-completed:
			c := pending[done.key]
			delete(pending, done.key)
			cancelled := c.ctx.Err() != nil
			c.cancel()
			if !cancelled {
				if err := enc.Encode(done.res); err != nil {
					return err
				}
			}
		case next := <-lines:
			if next.line == nil {
				lines = nil
				serveErr = next.err
				if next.err != nil {
					stop()
				}
				startShutdown()
				continue
			}
			var req request
			if err := json.Unmarshal(next.line, &req); err != nil {
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
				if req.Method == "notifications/cancelled" {
					var params struct {
						RequestID json.RawMessage `json:"requestId"`
						Reason    *string         `json:"reason"`
					}
					if json.Unmarshal(req.Params, &params) == nil {
						if c, ok := pending[stdioRequestKey(params.RequestID)]; ok {
							c.cancel()
						}
					}
				}
				continue
			}
			res := response{JSONRPC: "2.0", ID: req.ID}
			switch {
			case req.Method == "initialize" && initialized:
				res.Error = &rpcError{-32600, "already initialized"}
			case req.Method != "initialize" && req.Method != "ping" && !initialized:
				res.Error = &rpcError{-32002, "server not initialized"}
			case req.Method == "tools/call":
				key := stdioRequestKey(req.ID)
				if key == "" {
					res.Error = &rpcError{-32600, "invalid request"}
				} else if _, exists := pending[key]; exists {
					res.Error = &rpcError{-32600, "request ID already in flight"}
				} else if len(pending) >= maxStdioCalls {
					res.Error = &rpcError{-32000, "too many tool calls in flight"}
				} else {
					callCtx, cancel := context.WithCancel(runCtx)
					pending[key] = call{ctx: callCtx, cancel: cancel}
					go func() {
						res := response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{-32603, "internal error"}}
						defer func() {
							if recover() != nil {
								// Panic values can contain credentials or message bodies.
								log.Print("mcp: recovered panic in tool call")
							}
							completed <- completion{key: key, res: res}
						}()
						res = s.handle(callCtx, req)
					}()
					continue
				}
			default:
				res = s.handle(runCtx, req)
				if req.Method == "initialize" && res.Error == nil {
					initialized = true
				}
			}
			if err := enc.Encode(res); err != nil {
				return err
			}
		}
	}
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
		started := time.Now()
		requestID := comitia.NewRequestID()
		v, err := s.call(ctx, p.Name, p.Arguments)
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
		wrapped := map[string]any{"result": v}
		b, _ := json.Marshal(wrapped)
		r.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": string(b)}}, "structuredContent": wrapped, "isError": false}
	default:
		r.Error = &rpcError{-32601, "method not found"}
	}
	return r
}

func schema(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
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
	{serverInfoTools, (*Server).callServerInfo},
	{messageTools, (*Server).callMessages},
	{taskTools, (*Server).callTasks},
	{contextTools, (*Server).callContexts},
	{grantTools, (*Server).callGrants},
	{sessionTools, (*Server).callSessions},
	{presenceTools, (*Server).callPresence},
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
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return comitia.Invalidf("arguments must be a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		detail := comitia.InvalidJSON(err, "arguments")
		if strings.HasPrefix(err.Error(), "json: unknown field ") {
			build := comitia.BuildIdentity()
			return comitia.Invalidf("%s (this client is comitia %s, revision %s, dirty %s; restart the MCP client after upgrading)", strings.TrimPrefix(detail.Error(), "invalid input: "), build.Version, build.Revision, build.Dirty)
		}
		return detail
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return comitia.Invalidf("arguments must contain one JSON object")
	}
	return nil
}
func required(values ...string) error {
	for i := 0; i < len(values); i += 2 {
		v := values[i+1]
		if strings.TrimSpace(v) == "" {
			return comitia.Invalidf("%s required", values[i])
		}
	}
	return nil
}
func publicError(err error) error {
	if orphan := (orphanedSessionError{}); errors.As(err, &orphan) {
		return orphan
	}
	for _, e := range []error{comitia.ErrNotFound, comitia.ErrUnauthenticated, comitia.ErrForbidden, comitia.ErrConflict, comitia.ErrInvalid, comitia.ErrStaleClaim, comitia.ErrBootstrapUsed} {
		if errors.Is(err, e) {
			if errors.Is(err, comitia.ErrInvalid) {
				return err
			}
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
		return 0, comitia.Invalidf("lease_seconds must be between 1 and 86400")
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
	return nil, comitia.Invalidf("unknown tool")
}
