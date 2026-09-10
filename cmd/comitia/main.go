package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/mcp"
)

type config struct{ db, serverURL, credential, bootstrapCredential string }

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]string{"error": err.Error()})
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	global := flag.NewFlagSet("comitia", flag.ContinueOnError)
	global.SetOutput(os.Stderr)
	cfg := config{}
	global.StringVar(&cfg.db, "db", env("COMITIA_DB", "comitia.db"), "SQLite database path")
	global.StringVar(&cfg.serverURL, "server-url", env("COMITIA_SERVER_URL", ""), "remote Comitia server URL")
	// Secret defaults are applied after parsing so `--help` never prints values
	// sourced from the environment.
	global.StringVar(&cfg.credential, "credential", "", "bearer credential (or COMITIA_CREDENTIAL)")
	global.StringVar(&cfg.bootstrapCredential, "bootstrap-credential", "", "one-time local credential (or COMITIA_BOOTSTRAP_CREDENTIAL)")
	if err := global.Parse(args); err != nil {
		return err
	}
	if cfg.credential == "" {
		cfg.credential = os.Getenv("COMITIA_CREDENTIAL")
	}
	if cfg.bootstrapCredential == "" {
		cfg.bootstrapCredential = os.Getenv("COMITIA_BOOTSTRAP_CREDENTIAL")
	}
	rest := global.Args()
	if len(rest) == 0 {
		return usage()
	}
	cmd, args := rest[0], rest[1:]
	if cmd == "bootstrap" && cfg.serverURL != "" {
		return errors.New("bootstrap is available only with embedded SQLite")
	}
	api, closeFn, err := open(cfg)
	if err != nil {
		return err
	}
	defer closeFn()
	out := json.NewEncoder(os.Stdout)
	switch cmd {
	case "bootstrap":
		fs := newFlags(cmd)
		name := fs.String("name", "owner", "admin principal name")
		ttl := fs.Duration("ttl", 24*time.Hour, "session lifetime")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if cfg.bootstrapCredential == "" {
			return errors.New("bootstrap credential is required")
		}
		p, c, e := api.Bootstrap(ctx, comitia.BootstrapInput{Credential: cfg.bootstrapCredential, AdminName: *name, SessionTTL: *ttl})
		if e != nil {
			return e
		}
		return out.Encode(map[string]any{"principal": p, "credential": c})
	case "workspace-create":
		fs := newFlags(cmd)
		name := fs.String("name", "", "workspace name")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.CreateWorkspace(ctx, needCredential(cfg), *name)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "context-list":
		fs := newFlags(cmd)
		after := fs.String("after-id", "", "exclusive context ID cursor")
		limit := fs.Int("limit", 100, "maximum contexts (1-1000)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.ListContexts(ctx, needCredential(cfg), *after, *limit)
		if e != nil {
			return e
		}
		if v == nil {
			v = []comitia.WorkContext{}
		}
		return out.Encode(v)
	case "context-create":
		fs := newFlags(cmd)
		wid := fs.String("workspace-id", "", "workspace ID")
		name := fs.String("name", "", "context name")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.CreateContext(ctx, needCredential(cfg), comitia.CreateContextInput{WorkspaceID: *wid, Name: *name})
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "reference-add":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		kind := fs.String("kind", "", "reference kind")
		uri := fs.String("uri", "", "reference URI")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.AddReference(ctx, needCredential(cfg), comitia.AddReferenceInput{ContextID: *cid, Kind: *kind, URI: *uri})
		if e != nil {
			return e
		}
		return out.Encode(v)
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
	case "send":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		key := fs.String("dedupe-key", "", "stable deduplication key")
		body := fs.String("body", "", "message body")
		envelopeFile := fs.String("envelope-file", "", "validated message envelope JSON file")
		if err := fs.Parse(args); err != nil {
			return err
		}
		envelope, e := readEnvelope(*envelopeFile)
		if e != nil {
			return e
		}
		v, e := api.AppendMessage(ctx, needCredential(cfg), *cid, comitia.MessageInput{DedupeKey: *key, Body: *body, Envelope: envelope})
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "participants":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "work context ID")
		limit := fs.Int("limit", 100, "maximum participants (1-1000)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.ListParticipants(ctx, needCredential(cfg), *cid, *limit)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "replay":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		after := fs.Int64("after", 0, "exclusive sequence cursor")
		limit := fs.Int("limit", 100, "maximum messages")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.ReplayMessages(ctx, needCredential(cfg), *cid, *after, *limit)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "ack":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		seq := fs.Int64("sequence", 0, "inclusive sequence")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if e := api.AckMessages(ctx, needCredential(cfg), *cid, *seq); e != nil {
			return e
		}
		return out.Encode(map[string]bool{"ok": true})
	case "cursor":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.MessageCursor(ctx, needCredential(cfg), *cid)
		if e != nil {
			return e
		}
		return out.Encode(map[string]int64{"sequence": v})
	case "task-create":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		title := fs.String("title", "", "task title")
		body := fs.String("body", "", "task body")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.CreateTask(ctx, needCredential(cfg), *cid, comitia.TaskInput{Title: *title, Body: *body})
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-get":
		fs := newFlags(cmd)
		tid := fs.String("task-id", "", "task ID")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *tid == "" {
			return errors.New("task-id is required")
		}
		v, e := api.GetTask(ctx, needCredential(cfg), *tid)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-list":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		status := fs.String("status", "", "task status (pending, leased, completed)")
		limit := fs.Int("limit", 100, "maximum tasks (1-1000)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *cid == "" {
			return errors.New("context-id is required")
		}
		if *limit < 1 || *limit > 1000 {
			return comitia.ErrInvalid
		}
		st, e := parseTaskStatus(*status)
		if e != nil {
			return e
		}
		v, e := api.ListTasks(ctx, needCredential(cfg), *cid, st, *limit)
		if e != nil {
			return e
		}
		if v == nil {
			v = []comitia.Task{}
		}
		return out.Encode(v)
	case "task-claim":
		fs := newFlags(cmd)
		tid := fs.String("task-id", "", "task ID")
		lease := fs.Duration("lease", 5*time.Minute, "lease duration")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.ClaimTask(ctx, needCredential(cfg), *tid, *lease)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-renew":
		fs := newFlags(cmd)
		tid := fs.String("task-id", "", "task ID")
		token := fs.String("claim-token", "", "claim token")
		ver := fs.Int64("lease-version", 0, "lease version")
		lease := fs.Duration("lease", 5*time.Minute, "lease duration")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.RenewClaim(ctx, needCredential(cfg), *tid, *token, *ver, *lease)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "task-complete":
		fs := newFlags(cmd)
		tid := fs.String("task-id", "", "task ID")
		token := fs.String("claim-token", "", "claim token")
		ver := fs.Int64("lease-version", 0, "lease version")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.CompleteTask(ctx, needCredential(cfg), *tid, *token, *ver)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "serve":
		if cfg.serverURL != "" {
			return errors.New("serve requires embedded SQLite")
		}
		fs := newFlags(cmd)
		listen := fs.String("listen", env("COMITIA_LISTEN", "127.0.0.1:8080"), "listen address")
		if err := fs.Parse(args); err != nil {
			return err
		}
		return http.ListenAndServe(*listen, httpapi.NewHandler(api))
	case "mcp":
		if err := newFlags(cmd).Parse(args); err != nil {
			return err
		}
		return (&mcp.Server{API: api, Credential: needCredential(cfg)}).Serve(ctx, os.Stdin, os.Stdout)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func open(cfg config) (comitia.API, func(), error) {
	if cfg.serverURL != "" {
		c, e := httpapi.NewClient(cfg.serverURL, http.DefaultClient)
		return c, func() {}, e
	}
	store, e := comitia.OpenSQLite(cfg.db)
	if e != nil {
		return nil, func() {}, e
	}
	svc, e := comitia.New(comitia.Config{Store: store, BootstrapCredential: cfg.bootstrapCredential})
	if e != nil {
		store.Close()
		return nil, func() {}, e
	}
	return svc, func() { _ = store.Close() }, nil
}
func needCredential(c config) string { return c.credential }
func newFlags(name string) *flag.FlagSet {
	f := flag.NewFlagSet(name, flag.ContinueOnError)
	f.SetOutput(os.Stderr)
	return f
}
func usage() error {
	return errors.New("usage: comitia [--db PATH | --server-url URL] [--credential TOKEN] <bootstrap|workspace-create|context-create|context-list|reference-add|delegate|revoke|participants|send|replay|ack|cursor|task-create|task-get|task-list|task-claim|task-renew|task-complete|serve|mcp> [flags]")
}

func parseTaskStatus(s string) (comitia.TaskStatus, error) {
	switch s {
	case "":
		return "", nil
	case string(comitia.TaskPending):
		return comitia.TaskPending, nil
	case string(comitia.TaskLeased):
		return comitia.TaskLeased, nil
	case string(comitia.TaskCompleted):
		return comitia.TaskCompleted, nil
	default:
		return "", fmt.Errorf("invalid task status %q", s)
	}
}
func parsePermissions(s string) (comitia.Permission, error) {
	var p comitia.Permission
	for _, v := range strings.Split(s, ",") {
		switch strings.TrimSpace(v) {
		case "read":
			p |= comitia.PermissionRead
		case "write":
			p |= comitia.PermissionWrite
		case "claim":
			p |= comitia.PermissionClaim
		case "grant":
			p |= comitia.PermissionGrant
		case "admin":
			p |= comitia.PermissionAdmin
		case "all":
			p |= comitia.PermissionAll
		default:
			return 0, fmt.Errorf("unknown permission %q", v)
		}
	}
	return p, nil
}
