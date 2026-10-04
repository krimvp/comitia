package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/krimvp/comitia"
)

func runGrants(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	if cmd != "grant" {
		return errUnknownCommand
	}
	fs := newFlags(cmd)
	sid := fs.String("session-id", "", "target session ID")
	cid := fs.String("context-id", "", "context ID")
	perms := fs.String("permissions", "read", "comma-separated permissions")
	ttl := fs.Duration("ttl", time.Hour, "grant lifetime")
	if e := fs.Parse(args); e != nil {
		return e
	}
	if *sid == "" || *cid == "" || *ttl <= 0 {
		return fmt.Errorf("%w: session-id, context-id and positive ttl are required", comitia.ErrInvalid)
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
