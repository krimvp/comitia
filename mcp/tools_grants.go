package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/krimvp/comitia"
)

func permissionSchema() map[string]any {
	return map[string]any{"type": "array", "minItems": 1, "uniqueItems": true, "items": map[string]any{"type": "string", "enum": []string{"read", "write", "claim", "grant", "create"}}}
}

func parseToolPermissions(names []string) (comitia.Permission, error) {
	var bits comitia.Permission
	for _, name := range names {
		var bit comitia.Permission
		switch name {
		case "read":
			bit = comitia.PermissionRead
		case "write":
			bit = comitia.PermissionWrite
		case "claim":
			bit = comitia.PermissionClaim
		case "grant":
			bit = comitia.PermissionGrant
		case "create":
			bit = comitia.PermissionCreate
		default:
			return 0, comitia.ErrInvalid
		}
		if bits&bit != 0 {
			return 0, comitia.ErrInvalid
		}
		bits |= bit
	}
	if bits == 0 {
		return 0, comitia.ErrInvalid
	}
	return bits, nil
}

func grantTools() []map[string]any {
	return []map[string]any{
		tool("grant_context", "Grant context access to a direct child or invite a session already in the workspace. Contexts are the privacy boundary; non-admin grants only shrink permissions and expiry from one issuer grant containing grant.", schema(map[string]any{"session_id": str("Target session ID"), "context_id": str("Context ID"), "permissions": permissionSchema(), "ttl_seconds": integer("Positive grant lifetime in seconds")}, "session_id", "context_id", "permissions", "ttl_seconds")),
		tool("revoke_grant", "Revoke a grant and its descendants. Non-admin callers may revoke only grants issued by their own session; admins may revoke any grant. Delegated grants only shrink and revocation cascades.", schema(map[string]any{"grant_id": str("Grant ID")}, "grant_id")),
	}
}

func lifetimeSeconds(n int64) (time.Duration, error) {
	if n <= 0 || n > int64((1<<63-1)/time.Second) {
		return 0, comitia.ErrInvalid
	}
	return time.Duration(n) * time.Second, nil
}

func (s *Server) callGrants(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "grant_context":
		var a struct {
			SessionID   string   `json:"session_id"`
			ContextID   string   `json:"context_id"`
			Permissions []string `json:"permissions"`
			TTLSeconds  int64    `json:"ttl_seconds"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.SessionID, a.ContextID); err != nil {
			return nil, err
		}
		bits, err := parseToolPermissions(a.Permissions)
		if err != nil {
			return nil, err
		}
		ttl, err := lifetimeSeconds(a.TTLSeconds)
		if err != nil {
			return nil, err
		}
		return s.API.GrantContext(ctx, s.Credential, comitia.GrantInput{SessionID: a.SessionID, ContextID: a.ContextID, Permissions: bits, ExpiresAt: time.Now().UTC().Add(ttl)})
	case "revoke_grant":
		var a struct {
			GrantID string `json:"grant_id"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.GrantID); err != nil {
			return nil, err
		}
		if err := s.API.RevokeGrant(ctx, s.Credential, a.GrantID); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	default:
		return nil, errUnknownTool
	}
}
