// The grant commands: grant, grant-revoke and credential-check.

package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/krimvp/comitia"
)

func runGrants(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	if cmd == "credential-check" {
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context whose permissions to check")
		perms := fs.String("permissions", "read", "required comma-separated permissions")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if err := requireFlags(fs, "context-id"); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		p, err := parsePermissions(*perms)
		if err != nil {
			return err
		}
		checker, ok := api.(interface {
			CheckPermissions(context.Context, string, string, comitia.Permission) error
		})
		if !ok {
			return comitia.Invalidf("credential-check is available only with embedded SQLite")
		}
		if err := checker.CheckPermissions(ctx, needCredential(cfg), *cid, p); err != nil {
			return err
		}
		return out.Encode(map[string]bool{"ok": true})
	}
	if cmd == "grant-revoke" {
		fs := newFlags(cmd)
		id := fs.String("grant-id", "", "grant ID issued by this session (or any grant for admins)")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if err := requireFlags(fs, "grant-id"); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		if err := api.RevokeGrant(ctx, needCredential(cfg), *id); err != nil {
			return err
		}
		return out.Encode(map[string]bool{"ok": true})
	}
	if cmd != "grant" {
		return errUnknownCommand
	}
	fs := newFlags(cmd)
	sid := fs.String("session-id", "", "target session ID")
	cid := fs.String("context-id", "", "context ID")
	perms := fs.String("permissions", "read", "comma-separated permissions")
	ttl := fs.Duration("ttl", time.Hour, "grant lifetime")
	if e := parseFlags(fs, args); e != nil {
		return e
	}
	if e := requireFlags(fs, "session-id", "context-id"); e != nil {
		return e
	}
	if *ttl <= 0 {
		return comitia.Invalidf("grant requires a positive --ttl")
	}
	if cfg.parseOnly {
		return nil
	}
	p, e := parsePermissions(*perms)
	if e != nil {
		return e
	}
	g, e := api.GrantContext(ctx, needCredential(cfg), comitia.GrantInput{SessionID: *sid, ContextID: *cid, Permissions: p, ExpiresAt: time.Now().Add(*ttl)})
	if e != nil {
		return e
	}
	return out.Encode(g)
}
