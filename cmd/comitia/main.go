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

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
)

type config struct {
	db, serverURL, credential, bootstrapCredential string
	instances                                      comitia.InstanceDefaults
	maxContextDepth, maxChildContextsPerGrant      int
}

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

// commandGroups runs a command by domain; a group returns errUnknownCommand for commands it does not serve.
var commandGroups = []func(context.Context, config, comitia.API, *json.Encoder, string, []string) error{
	runServerInfo,
	runSessions,
	runContexts,
	runMessages,
	runTasks,
	runServe,
	runGrants,
	runPresence,
}
var errUnknownCommand = errors.New("unknown command")

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
	if usesInstanceDefaults(cmd) {
		instances, err := comitia.InstanceDefaultsFromEnv()
		if err != nil {
			return err
		}
		cfg.instances = instances
	}
	if cmd == "bootstrap" && cfg.serverURL != "" {
		return errors.New("bootstrap is available only with embedded SQLite")
	}
	if cmd == "serve" {
		_, depth, count, err := serveFlags(args)
		if err != nil {
			return err
		}
		cfg.maxContextDepth, cfg.maxChildContextsPerGrant = depth, count
	}
	api, closeFn, err := open(cfg)
	if err != nil {
		return err
	}
	defer closeFn()
	out := json.NewEncoder(os.Stdout)
	for _, group := range commandGroups {
		if err := group(ctx, cfg, api, out, cmd, args); err != errUnknownCommand {
			return err
		}
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func open(cfg config) (comitia.API, func(), error) {
	if cfg.serverURL != "" {
		c, e := httpapi.NewClientWithInstanceDefaults(cfg.serverURL, http.DefaultClient, cfg.instances)
		return c, func() {}, e
	}
	store, e := comitia.OpenSQLite(cfg.db)
	if e != nil {
		return nil, func() {}, e
	}
	svc, e := comitia.New(comitia.Config{Store: store, BootstrapCredential: cfg.bootstrapCredential, MaxContextDepth: cfg.maxContextDepth, MaxChildContextsPerGrant: cfg.maxChildContextsPerGrant})
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
	return errors.New("usage: comitia [--db PATH | --server-url URL] [--credential TOKEN] <server-info|bootstrap|workspace-create|context-create|context-list|context-children|reference-add|delegate|revoke|participants|instances|instance-resume|send|replay|wait|watch|ack|cursor|task-create|task-get|task-list|task-claim|task-renew|task-complete|task-release|serve|mcp|mcp-http|context-archive|grant|grant-revoke|credential-check> [flags]")
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
		case "create":
			p |= comitia.PermissionCreate
		case "all":
			p |= comitia.PermissionAll
		default:
			return 0, comitia.Invalidf("permissions must be read, write, claim, grant, admin, create or all")
		}
	}
	return p, nil
}

func usesInstanceDefaults(cmd string) bool {
	switch cmd {
	case "send", "replay", "wait", "cursor", "ack", "watch", "instance-resume", "mcp", "mcp-http", "task-claim":
		return true
	default:
		return false
	}
}
