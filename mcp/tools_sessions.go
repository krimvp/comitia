package mcp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/krimvp/comitia"
)

func sessionTools() []map[string]any {
	return []map[string]any{
		tool("delegate_session", "Create a helper session. Grants only shrink permissions and expiry from one issuer grant; contexts are the privacy boundary. Credentials go to private files on the MCP server, never to the model. Returns IDs, expiry and file path; does not launch a helper.", schema(map[string]any{"name": str("Helper principal name"), "ttl_seconds": integer("Positive session lifetime in seconds"), "grants": map[string]any{"type": "array", "minItems": 1, "items": schema(map[string]any{"context_id": str("Context ID"), "permissions": permissionSchema(), "ttl_seconds": integer("Positive grant lifetime, no longer than the session lifetime")}, "context_id", "permissions", "ttl_seconds")}}, "name", "grants", "ttl_seconds")),
		tool("revoke_session", "Revoke a session and its descendants. Non-admin callers may revoke only sessions they directly delegated; admins may revoke any session. Grant authority disappears with its session ancestry.", schema(map[string]any{"session_id": str("Session ID")}, "session_id")),
	}
}

type delegatedSessionResult struct {
	SessionID      string    `json:"session_id"`
	PrincipalID    string    `json:"principal_id"`
	ExpiresAt      time.Time `json:"expires_at"`
	GrantIDs       []string  `json:"grant_ids"`
	CredentialFile string    `json:"credential_file"`
}

func (s *Server) callSessions(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "delegate_session":
		var a struct {
			Name       string `json:"name"`
			TTLSeconds int64  `json:"ttl_seconds"`
			Grants     []struct {
				ContextID   string   `json:"context_id"`
				Permissions []string `json:"permissions"`
				TTLSeconds  int64    `json:"ttl_seconds"`
			} `json:"grants"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.Name); err != nil {
			return nil, err
		}
		ttl, err := lifetimeSeconds(a.TTLSeconds)
		if err != nil {
			return nil, err
		}
		if len(a.Grants) == 0 {
			return nil, comitia.ErrInvalid
		}
		in := comitia.DelegateInput{PrincipalName: a.Name, TTL: ttl}
		now := time.Now().UTC()
		for _, g := range a.Grants {
			if err := required(g.ContextID); err != nil {
				return nil, err
			}
			bits, err := parseToolPermissions(g.Permissions)
			if err != nil {
				return nil, err
			}
			grantTTL, err := lifetimeSeconds(g.TTLSeconds)
			if err != nil || grantTTL > ttl {
				return nil, comitia.ErrInvalid
			}
			in.Grants = append(in.Grants, comitia.DelegatedGrant{ContextID: g.ContextID, Permissions: bits, ExpiresAt: now.Add(grantTTL)})
		}
		// A credential file must point at the daemon backing this MCP server.
		endpoint, err := url.Parse(s.ServerURL)
		if err != nil || endpoint.Host == "" || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.User != nil {
			return nil, comitia.ErrInvalid
		}
		dir, err := privateSessionsDir(s.SessionsDir)
		if err != nil {
			return nil, err
		}
		p, c, grants, err := s.API.DelegateSession(ctx, s.Credential, in)
		if err != nil {
			return nil, err
		}
		path, err := writeSessionCredential(dir, s.ServerURL, p, c, grants)
		if err != nil {
			// The new session must not survive a failed credential handoff.
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			revokeErr := s.API.RevokeSession(cleanup, s.Credential, c.Session.ID)
			return nil, errors.Join(err, revokeErr)
		}
		result := delegatedSessionResult{SessionID: c.Session.ID, PrincipalID: p.ID, ExpiresAt: c.Session.ExpiresAt, GrantIDs: []string{}, CredentialFile: path}
		for _, g := range grants {
			result.GrantIDs = append(result.GrantIDs, g.ID)
		}
		return result, nil
	case "revoke_session":
		var a struct {
			SessionID string `json:"session_id"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required(a.SessionID); err != nil {
			return nil, err
		}
		if err := s.API.RevokeSession(ctx, s.Credential, a.SessionID); err != nil {
			return nil, err
		}
		return map[string]bool{"ok": true}, nil
	default:
		return nil, errUnknownTool
	}
}

func privateSessionsDir(dir string) (string, error) {
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(home, ".local", "share", "comitia", "sessions")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode().Perm() != 0700 {
		return "", errors.New("mcp: sessions directory must be private (0700)")
	}
	return dir, nil
}

func writeSessionCredential(dir, serverURL string, p comitia.Principal, c comitia.Credential, grants []comitia.Grant) (string, error) {
	if _, err := hex.DecodeString(c.Session.ID); err != nil || len(c.Session.ID) != 32 {
		return "", errors.New("mcp: invalid session ID")
	}
	path := filepath.Join(dir, c.Session.ID+".json")
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return "", err
	}
	if info.Mode().Perm() != 0700 {
		return "", errors.New("mcp: sessions directory must be private (0700)")
	}
	file, err := root.OpenFile(c.Session.ID+".json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	// Restore the required mode even when the process umask is more restrictive.
	err = file.Chmod(0600)
	if err == nil {
		err = json.NewEncoder(file).Encode(map[string]any{"principal": p, "credential": c, "grants": grants, "server_url": serverURL})
	}
	if err == nil {
		err = file.Sync()
	}
	err = errors.Join(err, file.Close())
	if err != nil {
		_ = root.Remove(c.Session.ID + ".json")
		return "", err
	}
	return path, nil
}
