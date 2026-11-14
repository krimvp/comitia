// The session commands: bootstrap, delegate and revoke.

package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

func runSessions(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	switch cmd {
	case "bootstrap":
		fs := newFlags(cmd)
		name := fs.String("name", "owner", "admin principal name")
		ttl := fs.Duration("ttl", 24*time.Hour, "session lifetime")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if cfg.bootstrapCredential == "" {
			return comitia.Invalidf("bootstrap requires --bootstrap-credential (or COMITIA_BOOTSTRAP_CREDENTIAL)")
		}
		if cfg.parseOnly {
			return nil
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
		var grantSpecs repeatedGrants
		fs.Var(&grantSpecs, "grant", "repeat CONTEXT_ID:PERMISSIONS:TTL for atomic multi-context delegation; excludes --context-id/--permissions/--grant-ttl")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if err := requireAny(fs, "context-id", "grant"); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		var grants []comitia.DelegatedGrant
		now := time.Now()
		if len(grantSpecs) > 0 {
			if flagProvided(fs, "context-id") || flagProvided(fs, "permissions") || flagProvided(fs, "grant-ttl") {
				return comitia.Invalidf("--grant excludes --context-id, --permissions and --grant-ttl")
			}
			for _, spec := range grantSpecs {
				parts := strings.Split(spec, ":")
				if len(parts) != 3 || parts[0] == "" {
					return comitia.Invalidf("grant must be CONTEXT_ID:PERMISSIONS:TTL")
				}
				bits, err := parsePermissions(parts[1])
				if err != nil {
					return err
				}
				lifetime, err := time.ParseDuration(parts[2])
				if err != nil || lifetime <= 0 || lifetime > *ttl {
					return comitia.Invalidf("grant TTL must be positive and no longer than session TTL")
				}
				grants = append(grants, comitia.DelegatedGrant{ContextID: parts[0], Permissions: bits, ExpiresAt: now.Add(lifetime)})
			}
		} else {
			bits, err := parsePermissions(*perms)
			if err != nil {
				return err
			}
			grants = []comitia.DelegatedGrant{{ContextID: *cid, Permissions: bits, ExpiresAt: now.Add(*grantTTL)}}
		}
		p, c, g, e := api.DelegateSession(ctx, needCredential(cfg), comitia.DelegateInput{PrincipalName: *name, TTL: *ttl, Grants: grants})
		if e != nil {
			return e
		}
		return out.Encode(map[string]any{"principal": p, "credential": c, "grants": g})
	case "revoke":
		fs := newFlags(cmd)
		sid := fs.String("session-id", "", "session ID")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if err := requireFlags(fs, "session-id"); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		if e := api.RevokeSession(ctx, needCredential(cfg), *sid); e != nil {
			return e
		}
		return out.Encode(map[string]bool{"ok": true})
	default:
		return errUnknownCommand
	}
}

// repeatedGrants leaves attenuation and all-or-nothing persistence to the service.
type repeatedGrants []string

func (g *repeatedGrants) String() string         { return strings.Join(*g, ";") }
func (g *repeatedGrants) Set(value string) error { *g = append(*g, value); return nil }
