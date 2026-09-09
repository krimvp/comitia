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

// Server binds one already-scoped credential to an MCP connection. Credentials
// are process configuration and are never accepted as tool arguments.
type Server struct {
	API        comitia.API
	Credential string
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

func tools() []map[string]any {
	return []map[string]any{
		tool("send_message", "Append a message; optional validated envelope addresses session IDs, not names. Context readers can see all messages.", schema(map[string]any{"context_id": str("Context ID"), "dedupe_key": str("Stable caller-chosen key"), "body": str("Message explanation; required for untyped/discussion messages"), "envelope": envelopeSchema()}, "context_id", "dedupe_key")),
		tool("list_participants", "Discover active readable sessions in a context. Names may repeat; address by SessionID.", schema(map[string]any{"context_id": str("Context ID"), "limit": integer("Maximum participants (default 100)")}, "context_id")),
		tool("replay_messages", "Read context messages after a sequence", schema(map[string]any{"context_id": str("Context ID"), "after": integer("Exclusive sequence cursor"), "limit": integer("Maximum messages")}, "context_id")),
		tool("ack_messages", "Record consumption through a sequence", schema(map[string]any{"context_id": str("Context ID"), "sequence": integer("Inclusive sequence")}, "context_id", "sequence")),
		tool("message_cursor", "Read this session's persisted acknowledgement cursor", schema(map[string]any{"context_id": str("Context ID")}, "context_id")),
		tool("create_task", "Create a task in a context", schema(map[string]any{"context_id": str("Context ID"), "title": str("Task title"), "body": str("Task details")}, "context_id", "title")),
		tool("claim_task", "Claim a pending task", schema(map[string]any{"task_id": str("Task ID"), "lease_seconds": integer("Lease duration in seconds")}, "task_id")),
		tool("renew_task", "Renew a task claim", schema(map[string]any{"task_id": str("Task ID"), "claim_token": str("Opaque claim token"), "lease_version": integer("Current lease version"), "lease_seconds": integer("Lease duration in seconds")}, "task_id", "claim_token", "lease_version")),
		tool("complete_task", "Complete a claimed task", schema(map[string]any{"task_id": str("Task ID"), "claim_token": str("Opaque claim token"), "lease_version": integer("Current lease version")}, "task_id", "claim_token", "lease_version")),
		tool("get_task", "Get a task by ID", schema(map[string]any{"task_id": str("Task ID")}, "task_id")),
		tool("list_tasks", "List tasks in a context", schema(map[string]any{"context_id": str("Context ID"), "status": map[string]any{"type": "string", "enum": []string{"pending", "leased", "completed"}}, "limit": integer("Maximum tasks")}, "context_id")),
	}
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

func (s *Server) call(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "send_message":
		var a struct {
			ContextID string                   `json:"context_id"`
			DedupeKey string                   `json:"dedupe_key"`
			Body      string                   `json:"body"`
			Envelope  *comitia.MessageEnvelope `json:"envelope"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.ContextID, a.DedupeKey); err != nil {
			return nil, err
		}
		return s.API.AppendMessage(ctx, s.Credential, a.ContextID, comitia.MessageInput{DedupeKey: a.DedupeKey, Body: a.Body, Envelope: a.Envelope})
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
	case "replay_messages":
		var a struct {
			ContextID string `json:"context_id"`
			After     int64  `json:"after"`
			Limit     int    `json:"limit"`
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
		return s.API.ReplayMessages(ctx, s.Credential, a.ContextID, a.After, a.Limit)
	case "ack_messages":
		var a struct {
			ContextID string `json:"context_id"`
			Sequence  int64  `json:"sequence"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.ContextID); err != nil {
			return nil, err
		}
		if err := s.API.AckMessages(ctx, s.Credential, a.ContextID, a.Sequence); err != nil {
			return nil, err
		}
		return map[string]any{"ok": true}, nil
	case "message_cursor":
		var a struct {
			ContextID string `json:"context_id"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.ContextID); err != nil {
			return nil, err
		}
		v, err := s.API.MessageCursor(ctx, s.Credential, a.ContextID)
		if err != nil {
			return nil, err
		}
		return map[string]int64{"sequence": v}, nil
	case "create_task":
		var a struct {
			ContextID string `json:"context_id"`
			Title     string `json:"title"`
			Body      string `json:"body"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.ContextID, a.Title); err != nil {
			return nil, err
		}
		return s.API.CreateTask(ctx, s.Credential, a.ContextID, comitia.TaskInput{Title: a.Title, Body: a.Body})
	case "claim_task":
		var a struct {
			TaskID       string `json:"task_id"`
			LeaseSeconds int64  `json:"lease_seconds"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.TaskID); err != nil {
			return nil, err
		}
		d, e := seconds(a.LeaseSeconds, 300)
		if e != nil {
			return nil, e
		}
		return s.API.ClaimTask(ctx, s.Credential, a.TaskID, d)
	case "renew_task":
		var a struct {
			TaskID       string `json:"task_id"`
			ClaimToken   string `json:"claim_token"`
			LeaseVersion int64  `json:"lease_version"`
			LeaseSeconds int64  `json:"lease_seconds"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.TaskID, a.ClaimToken); err != nil {
			return nil, err
		}
		d, e := seconds(a.LeaseSeconds, 300)
		if e != nil {
			return nil, e
		}
		return s.API.RenewClaim(ctx, s.Credential, a.TaskID, a.ClaimToken, a.LeaseVersion, d)
	case "complete_task":
		var a struct {
			TaskID       string `json:"task_id"`
			ClaimToken   string `json:"claim_token"`
			LeaseVersion int64  `json:"lease_version"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.TaskID, a.ClaimToken); err != nil {
			return nil, err
		}
		return s.API.CompleteTask(ctx, s.Credential, a.TaskID, a.ClaimToken, a.LeaseVersion)
	case "get_task":
		var a struct {
			TaskID string `json:"task_id"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.TaskID); err != nil {
			return nil, err
		}
		return s.API.GetTask(ctx, s.Credential, a.TaskID)
	case "list_tasks":
		var a struct {
			ContextID string `json:"context_id"`
			Status    string `json:"status"`
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
		status := comitia.TaskStatus(a.Status)
		if a.Status != "" && status != comitia.TaskPending && status != comitia.TaskLeased && status != comitia.TaskCompleted {
			return nil, comitia.ErrInvalid
		}
		return s.API.ListTasks(ctx, s.Credential, a.ContextID, status, limit)
	default:
		return nil, comitia.ErrInvalid
	}
}
