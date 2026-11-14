// The CLI entry point: main and run build the configuration from global flags
// and the environment, then dispatch the command to a command group
// (cmd_*.go).

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/krimvp/comitia"
)

type config struct {
	db, serverURL, credential, bootstrapCredential string
	instances                                      comitia.InstanceDefaults
	maxContextDepth, maxChildContextsPerGrant      int
	parseOnly                                      bool
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		_ = json.NewEncoder(os.Stderr).Encode(map[string]string{"error": err.Error()})
		os.Exit(exitCode(err))
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
	version := global.Bool("version", false, "print build identity and exit")
	global.Usage = func() {
		fmt.Fprint(global.Output(), commandUsage)
		global.PrintDefaults()
	}
	if err := parseQuiet(global, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *version {
		return json.NewEncoder(os.Stdout).Encode(comitia.BuildIdentity())
	}
	if cfg.credential == "" {
		cfg.credential = os.Getenv("COMITIA_CREDENTIAL")
	}
	if cfg.bootstrapCredential == "" {
		cfg.bootstrapCredential = os.Getenv("COMITIA_BOOTSTRAP_CREDENTIAL")
	}
	rest := global.Args()
	if len(rest) == 0 {
		global.Usage()
		return comitia.Invalidf("command required")
	}
	cmd, args := rest[0], rest[1:]
	if cmd == "help" || cmd == "version" {
		if err := parseFlags(newFlags(cmd), args); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if cmd == "help" {
			global.Usage()
			return nil
		}
		return json.NewEncoder(os.Stdout).Encode(comitia.BuildIdentity())
	}
	// Ask the same command handlers to parse flags only. Help and
	// parsing errors must finish before storage, network or runtime validation.
	parseCfg := cfg
	parseCfg.parseOnly = true
	if err := dispatch(ctx, parseCfg, nil, nil, cmd, args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if usesInstanceDefaults(cmd) {
		instances, err := comitia.InstanceDefaultsFromEnv()
		if err != nil {
			return err
		}
		cfg.instances = instances
	}
	if cmd == "bootstrap" && cfg.serverURL != "" {
		return comitia.Invalidf("bootstrap is available only with embedded SQLite")
	}
	if cmd == "serve" {
		_, depth, count, _, err := serveFlags(args)
		if err != nil {
			return err
		}
		cfg.maxContextDepth, cfg.maxChildContextsPerGrant = depth, count
	}
	// Only bootstrap and serve may create a database; elsewhere a missing file
	// usually means the remote server URL was forgotten.
	if cfg.serverURL == "" && cmd != "bootstrap" && cmd != "serve" && cfg.db != ":memory:" {
		if _, err := os.Stat(cfg.db); os.IsNotExist(err) {
			return comitia.Invalidf("no database at %s; set --server-url (or COMITIA_SERVER_URL) for a remote server, or --db (or COMITIA_DB) for an existing database", cfg.db)
		}
	}
	api, closeFn, err := open(cfg)
	if err != nil {
		return err
	}
	defer closeFn()
	out := json.NewEncoder(os.Stdout)
	return dispatch(ctx, cfg, api, out, cmd, args)
}

func dispatch(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	for _, group := range commandGroups {
		if err := group(ctx, cfg, api, out, cmd, args); err != errUnknownCommand {
			return err
		}
	}
	return comitia.Invalidf("unknown command %q; see comitia --help", cmd)
}

func needCredential(c config) string { return c.credential }

func usesInstanceDefaults(cmd string) bool {
	switch cmd {
	case "send", "replay", "wait", "cursor", "ack", "watch", "instance-resume", "mcp", "mcp-http", "task-claim", "task-recover", "task-list":
		return true
	default:
		return false
	}
}
