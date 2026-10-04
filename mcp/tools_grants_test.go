package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/krimvp/comitia"
)

func TestManagementToolsStrictArguments(t *testing.T) {
	validGrant := map[string]any{"session_id": "target", "context_id": "context", "permissions": []string{"read"}, "ttl_seconds": 60}
	for _, permissions := range []any{nil, []string{}, []string{"read", "read"}, []string{"admin"}, []string{"all"}, []string{"Read"}, []string{" read"}, []string{"unknown"}, "read", []any{1}, []any{nil}} {
		args := map[string]any{}
		for k, v := range validGrant {
			args[k] = v
		}
		args["permissions"] = permissions
		raw, _ := json.Marshal(args)
		if _, err := (&Server{}).call(context.Background(), "grant_context", raw); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("permissions %v: %v", permissions, err)
		}
		delegate := map[string]any{"name": "helper", "ttl_seconds": 60, "grants": []any{map[string]any{"context_id": "context", "permissions": permissions, "ttl_seconds": 30}}}
		raw, _ = json.Marshal(delegate)
		if _, err := (&Server{}).call(context.Background(), "delegate_session", raw); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("delegate permissions %v: %v", permissions, err)
		}
	}
	for _, tc := range []struct{ name, args string }{
		{"grant_context", `{"session_id":"s","context_id":"c","permissions":["read"]}`},
		{"grant_context", `{"session_id":"s","context_id":"c","permissions":["read"],"ttl_seconds":0}`},
		{"grant_context", `{"session_id":"s","context_id":"c","permissions":["read"],"ttl_seconds":-1}`},
		{"grant_context", `{"session_id":"s","context_id":"c","permissions":["read"],"ttl_seconds":9223372036854775807}`},
		{"grant_context", `{"session_id":"s","context_id":"c","permissions":["read"],"ttl_seconds":1.5}`},
		{"grant_context", `{"session_id":"s","context_id":"c","permissions":["read"],"ttl_seconds":1,"credential":"injected"}`},
		{"delegate_session", `{"name":"helper","ttl_seconds":60,"grants":[]}`},
		{"delegate_session", `{"name":"helper","ttl_seconds":60,"grants":[{"context_id":"c","permissions":["read"],"ttl_seconds":61}]}`},
		{"delegate_session", `{"name":"helper","ttl_seconds":60,"grants":[{"context_id":"c","permissions":["read"],"ttl_seconds":0}]}`},
		{"delegate_session", `{"name":"helper","ttl_seconds":60,"grants":[{"context_id":"c","permissions":["read"],"ttl_seconds":30,"token":"injected"}]}`},
		{"delegate_session", `{"name":"helper","ttl_seconds":9223372036854775807,"grants":[]}`},
		{"revoke_session", `{}`}, {"revoke_grant", `{"grant_id":" "}`},
		{"list_contexts", `{"limit":0}`}, {"list_contexts", `{"limit":1001}`}, {"list_contexts", `{"limit":-1}`},
		{"list_contexts", `{"parent_context_id":"c"}`}, {"list_child_contexts", `{}`},
		{"create_context", `{"parent_context_id":"c","name":" "}`},
	} {
		t.Run(tc.name+tc.args, func(t *testing.T) {
			if _, err := (&Server{}).call(context.Background(), tc.name, json.RawMessage(tc.args)); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("validation: %v", err)
			}
		})
	}
	bits, err := parseToolPermissions("permissions", []string{"read", "write", "claim", "grant", "create"})
	want := comitia.PermissionRead | comitia.PermissionWrite | comitia.PermissionClaim | comitia.PermissionGrant | comitia.PermissionCreate
	if err != nil || bits != want {
		t.Fatalf("bits=%v err=%v", bits, err)
	}
}

func TestDelegationCannotExpandThroughMCP(t *testing.T) {
	s, _, _, parent := contextServer(t, true)
	for _, args := range []map[string]any{
		{"name": "session-too-long", "ttl_seconds": 9 * 3600, "grants": []any{map[string]any{"context_id": parent.ID, "permissions": []string{"read"}, "ttl_seconds": 60}}},
		{"name": "grant-too-long", "ttl_seconds": 8*3600 - 60, "grants": []any{map[string]any{"context_id": parent.ID, "permissions": []string{"read"}, "ttl_seconds": 7*3600 + 60}}},
		{"name": "foreign-context", "ttl_seconds": 60, "grants": []any{map[string]any{"context_id": "unknown", "permissions": []string{"read"}, "ttl_seconds": 30}}},
	} {
		raw, _ := json.Marshal(args)
		if _, err := s.call(context.Background(), "delegate_session", raw); !errors.Is(err, comitia.ErrForbidden) {
			t.Fatalf("expansion: %v", err)
		}
	}
	helper := callTool(t, s, "delegate_session", map[string]any{"name": "reader", "ttl_seconds": 600, "grants": []any{map[string]any{"context_id": parent.ID, "permissions": []string{"read", "grant"}, "ttl_seconds": 300}}}).(delegatedSessionResult)
	data, err := os.ReadFile(helper.CredentialFile)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Credential comitia.Credential `json:"credential"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	s.Credential = doc.Credential.Token
	raw, _ := json.Marshal(map[string]any{"name": "writer", "ttl_seconds": 120, "grants": []any{map[string]any{"context_id": parent.ID, "permissions": []string{"write"}, "ttl_seconds": 60}}})
	if _, err := s.call(context.Background(), "delegate_session", raw); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("permission expansion: %v", err)
	}
}
