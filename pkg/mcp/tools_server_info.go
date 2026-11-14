// Server info tools (server_info, storage_status) and the unknown-field hint
// that consults server_info to tell a stale client from a misspelt field.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/krimvp/comitia/pkg/comitia"
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
	info, err := s.API.ServerInfo(ctx, s.Credential)
	if err != nil {
		return nil, err
	}
	return info.WithClient(comitia.BuildIdentity()), nil
}

// unknownFieldError is an argument the running tool schema does not know. Its
// guidance depends on whether this client is older than the server, which only
// the Server can find out (see withUnknownFieldHint).
type unknownFieldError struct {
	detail string
	error
}

func newUnknownFieldError(detail string) error {
	return unknownFieldError{detail, comitia.Invalidf("%s (%s; check the field name against the tool schema, or call server_info to see whether the MCP client needs a restart)", detail, clientIdentity())}
}
func (e unknownFieldError) Unwrap() error { return e.error }

func clientIdentity() string {
	build := comitia.BuildIdentity()
	return fmt.Sprintf("this client is comitia %s, revision %s, dirty %s", build.Version, build.Revision, build.Dirty)
}

// withUnknownFieldHint suggests a restart only when server_info shows this
// client is stale; otherwise the field name itself is the likely problem.
func (s *Server) withUnknownFieldHint(ctx context.Context, err error) error {
	var unknown unknownFieldError
	if !errors.As(err, &unknown) {
		return err
	}
	info, ok := s.clientStatus(ctx)
	if !ok {
		return err
	}
	if !*info.ClientStale {
		return comitia.Invalidf("%s (%s, matching the server; check the field name against the tool schema)", unknown.detail, clientIdentity())
	}
	return comitia.Invalidf("%s (%s; server is comitia %s, revision %s; %s)", unknown.detail, clientIdentity(), info.Build.Version, info.Build.Revision, info.ClientHint)
}

// clientStatus is best effort: an API that cannot answer server_info, including
// a partial implementation that panics, leaves the generic unknown-field hint.
func (s *Server) clientStatus(ctx context.Context) (info comitia.ServerInfo, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	info, err := s.API.ServerInfo(ctx, s.Credential)
	if err != nil {
		return info, false
	}
	return info.WithClient(comitia.BuildIdentity()), true
}
