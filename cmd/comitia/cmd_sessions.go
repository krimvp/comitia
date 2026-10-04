package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/krimvp/comitia"
)

func runSessions(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	switch cmd {
	case "bootstrap":
		fs := newFlags(cmd)
		name := fs.String("name", "owner", "admin principal name")
		ttl := fs.Duration("ttl", 24*time.Hour, "session lifetime")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if cfg.bootstrapCredential == "" {
			return comitia.Invalidf("bootstrap credential is required")
		}
		p, c, e := api.Bootstrap(ctx, comitia.BootstrapInput{Credential: cfg.bootstrapCredential, AdminName: *name, SessionTTL: *ttl})
		if e != nil {
			return e
		}
		return out.Encode(map[string]any{"principal": p, "credential": c})
	case "delegate":
		fs := newFlags(cmd)
		name := fs.String("name", "agent", "child principal name")
		ttl := fs.Duration("ttl", time.Hour, "session lifetime")
		cid := fs.String("context-id", "", "context ID")
		perms := fs.String("permissions", "read,write,claim", "comma-separated permissions")
		grantTTL := fs.Duration("grant-ttl", time.Hour, "grant lifetime")
		if err := fs.Parse(args); err != nil {
			return err
		}
		pbits, e := parsePermissions(*perms)
		if e != nil {
			return e
		}
		p, c, g, e := api.DelegateSession(ctx, needCredential(cfg), comitia.DelegateInput{PrincipalName: *name, TTL: *ttl, Grants: []comitia.DelegatedGrant{{ContextID: *cid, Permissions: pbits, ExpiresAt: time.Now().Add(*grantTTL)}}})
		if e != nil {
			return e
		}
		return out.Encode(map[string]any{"principal": p, "credential": c, "grants": g})
	case "revoke":
		fs := newFlags(cmd)
		sid := fs.String("session-id", "", "session ID")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if e := api.RevokeSession(ctx, needCredential(cfg), *sid); e != nil {
			return e
		}
		return out.Encode(map[string]bool{"ok": true})
	default:
		return errUnknownCommand
	}
}
