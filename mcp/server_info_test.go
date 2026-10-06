package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
)

func TestValidationDetailOverMCP(t *testing.T) {
	tests := []struct{ raw, detail string }{
		{`{"agent_instance":{"id":"worker"}}`, `unknown field "agent_instance"`},
		{`{"task_id":"t","credential":"secret-value"}`, `unknown field "credential"`},
		{`{}`, "task_id required"},
		{`{"task_id":123}`, "task_id must have type string"},
		{`null`, "arguments must be a JSON object"},
		{`{} {}`, "arguments must contain one JSON object"},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			s := &Server{API: &fakeAPI{}, Credential: "fixed-secret"}
			_, err := s.call(context.Background(), "get_task", json.RawMessage(tc.raw))
			if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(publicError(err).Error(), tc.detail) {
				t.Fatalf("error=%v", err)
			}
			if strings.Contains(tc.detail, "unknown field") && !strings.Contains(err.Error(), "this client is comitia "+comitia.BuildVersion()) {
				t.Fatalf("missing version: %v", err)
			}
			input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}` + "\n" +
				`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_task","arguments":` + tc.raw + `}}` + "\n"
			// Multiple JSON values are tested through decoding above; they cannot
			// form a well-formed outer JSON-RPC request.
			if tc.raw == `{} {}` {
				return
			}
			got := serve(t, &fakeAPI{}, input)
			body, _ := json.Marshal(got[1])
			var result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			}
			raw, _ := json.Marshal(got[1]["result"])
			if json.Unmarshal(raw, &result) != nil || !result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, tc.detail) {
				t.Fatalf("response=%s", body)
			}
			if strings.Contains(string(body), "secret-value") || strings.Contains(string(body), "fixed-secret") {
				t.Fatalf("secret leaked: %s", body)
			}
		})
	}
	wrapped := comitia.Invalidf("task_id required")
	if publicError(wrapped) != wrapped {
		t.Fatal("validation detail lost")
	}
	if got := publicError(errors.New("secret-value")); got.Error() != "internal error" {
		t.Fatalf("internal error=%v", got)
	}
}

func TestToolValidationDetails(t *testing.T) {
	tests := []struct{ tool, raw, detail string }{
		{"wait_messages", `{"context_id":"c","timeout":5}`, `unknown field "timeout"`},
		{"wait_messages", `{}`, "context_id required"},
		{"wait_messages", `{"context_id":"c","timeout_seconds":-1}`, "timeout_seconds must be a finite nonnegative number"},
		{"replay_messages", `{"context_id":"c","to_instance":"x"}`, `unknown field "to_instance"`},
		{"create_context", `{"parent_context_id":"p","name":"n","private":true}`, `unknown field "private"`},
		{"create_context", `{"parent_context_id":"p"}`, "name required"},
		{"list_contexts", `{"parent_context_id":"p"}`, `unknown field "parent_context_id"`},
		{"list_child_contexts", `{}`, "parent_context_id required"},
		{"list_child_contexts", `{"parent_context_id":"p","limit":0}`, "limit must be between 1 and 1000"},
		{"grant_context", `{"session_id":"s","context_id":"c","permissions":["read"],"ttl_seconds":60,"expires_at":"x"}`, `unknown field "expires_at"`},
		{"grant_context", `{"context_id":"c"}`, "session_id required"},
		{"grant_context", `{"session_id":"s","context_id":"c","permissions":["admin"],"ttl_seconds":60}`, "permissions must contain only"},
		{"grant_context", `{"session_id":"s","context_id":"c","permissions":["read","read"],"ttl_seconds":60}`, "permissions must not repeat"},
		{"grant_context", `{"session_id":"s","context_id":"c","permissions":["read"]}`, "ttl_seconds must be a positive number of seconds"},
		{"revoke_grant", `{}`, "grant_id required"},
		{"delegate_session", `{"name":"h","ttl_seconds":60,"grants":[]}`, "grants must not be empty"},
		{"delegate_session", `{"name":"h","ttl_seconds":60,"grants":[{"context_id":"c","permissions":[],"ttl_seconds":60}]}`, "grants[].permissions must not be empty"},
		{"delegate_session", `{"name":"h","ttl_seconds":60,"grants":[{"context_id":"c","permissions":["read"],"ttl_seconds":61}]}`, "grants[].ttl_seconds must not exceed ttl_seconds"},
		{"delegate_session", `{"name":"h","ttl_seconds":60,"grants":[{"context_id":"c","permissions":["read"],"ttl_seconds":60,"token":"x"}]}`, `unknown field "token"`},
		{"revoke_session", `{}`, "session_id required"},
	}
	for _, tc := range tests {
		t.Run(tc.tool+" "+tc.raw, func(t *testing.T) {
			s := &Server{API: &fakeAPI{}, Credential: "fixed-secret"}
			_, err := s.call(context.Background(), tc.tool, json.RawMessage(tc.raw))
			if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(publicError(err).Error(), tc.detail) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestServerInfoMCP(t *testing.T) {
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "local"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "local", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{API: svc, Credential: owner.Token}
	v, err := s.call(ctx, "server_info", json.RawMessage(`{}`))
	if err != nil || v.(comitia.ServerInfo).ProtocolVersion != ProtocolVersion {
		t.Fatalf("info=%v err=%v", v, err)
	}
	s.Credential = "invalid-secret"
	if _, err := s.call(ctx, "server_info", json.RawMessage(`{}`)); !errors.Is(err, comitia.ErrUnauthenticated) {
		t.Fatalf("auth=%v", err)
	}
	listed := false
	for _, tool := range tools() {
		if tool["name"] == "server_info" {
			listed = true
		}
	}
	if !listed {
		t.Fatal("tool not listed")
	}
}

type infoAPI struct {
	fakeAPI
	info comitia.ServerInfo
	err  error
}

func (a *infoAPI) ServerInfo(context.Context, string) (comitia.ServerInfo, error) {
	return a.info, a.err
}

func TestStaleClientMCP(t *testing.T) {
	client := comitia.BuildIdentity()
	newer := comitia.ServerInfo{Version: "dev", ProtocolVersion: ProtocolVersion, Features: append(comitia.Features(), "future_feature"), Build: comitia.BuildInfo{Version: "dev", Revision: "newer-revision", Dirty: "false"}}
	current := comitia.ServerInfo{Version: client.Version, ProtocolVersion: ProtocolVersion, Features: comitia.Features(), Build: client}
	tests := []struct {
		name        string
		info        comitia.ServerInfo
		err         error
		stale       bool
		want, avoid string
	}{
		{"newer server", newer, nil, true, "server is comitia dev, revision newer-revision; the server is newer than this MCP client; restart the MCP client", "check the field name"},
		{"same build", current, nil, false, "matching the server; check the field name against the tool schema", "restart"},
		{"server_info unavailable", comitia.ServerInfo{}, comitia.ErrUnauthenticated, false, "check the field name against the tool schema, or call server_info", "restart the MCP client ("},
	}
	// A partial API without ServerInfo keeps the generic hint instead of panicking.
	s := &Server{API: &fakeAPI{}, Credential: "fixed-secret"}
	if _, err := s.call(context.Background(), "get_task", json.RawMessage(`{"after_sequence":1}`)); !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "or call server_info") {
		t.Fatalf("partial API=%v", err)
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := &Server{API: &infoAPI{info: tc.info, err: tc.err}, Credential: "fixed-secret"}
			if tc.err == nil {
				v, err := s.call(context.Background(), "server_info", json.RawMessage(`{}`))
				info, _ := v.(comitia.ServerInfo)
				if err != nil || info.Client == nil || *info.Client != client || *info.ClientStale != tc.stale || info.Build != tc.info.Build {
					t.Fatalf("info=%+v err=%v", info, err)
				}
				if tc.stale && (!reflect.DeepEqual(info.ClientMissingFeatures, []string{"future_feature"}) || !strings.Contains(info.ClientHint, "restart the MCP client")) {
					t.Fatalf("info=%+v", info)
				}
				raw, _ := json.Marshal(info)
				if !strings.Contains(string(raw), `"client_stale":`+map[bool]string{true: "true", false: "false"}[tc.stale]) {
					t.Fatalf("wire=%s", raw)
				}
			}
			_, err := s.call(context.Background(), "get_task", json.RawMessage(`{"task_id":"t","after_sequence":1}`))
			text := publicError(err).Error()
			if !errors.Is(err, comitia.ErrInvalid) || !strings.HasPrefix(text, `invalid input: unknown field "after_sequence"`) || !strings.Contains(text, `(this client is comitia `+client.Version) || !strings.Contains(text, tc.want) || strings.Contains(text, tc.avoid) || strings.Contains(text, "fixed-secret") {
				t.Fatalf("error=%v", text)
			}
		})
	}
}
