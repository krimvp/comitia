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

// commandGroups runs a command by domain; a group returns errUnknownCommand for commands it does not serve.
var commandGroups = []func(context.Context, config, comitia.API, *json.Encoder, string, []string) error{
	runSessions,
	runContexts,
	runMessages,
	runTasks,
	runServe,
	runGrants,
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
	if cmd == "bootstrap" && cfg.serverURL != "" {
		return errors.New("bootstrap is available only with embedded SQLite")
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
	return errors.New("usage: comitia [--db PATH | --server-url URL] [--credential TOKEN] <bootstrap|workspace-create|context-create|context-list|context-children|reference-add|delegate|revoke|participants|send|replay|ack|cursor|task-create|task-get|task-list|task-claim|task-renew|task-complete|serve|mcp|mcp-http|grant> [flags]")
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
			return 0, fmt.Errorf("unknown permission %q", v)
		}
	}
	return p, nil
}
