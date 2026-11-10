package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/sqlite"
)

type config struct {
	db, serverURL, credential, bootstrapCredential string
	instances                                      comitia.InstanceDefaults
	maxContextDepth, maxChildContextsPerGrant      int
	parseOnly                                      bool
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
		os.Exit(exitCode(err))
	}
}

// Exit codes by error class; docs/USING-COMITIA.md lists them. Unclassified
// failures keep the historical 1.
const (
	exitInternal        = 1
	exitUsage           = 2 // invalid flags, arguments or input
	exitUnauthenticated = 3
	exitForbidden       = 4
	exitNotFound        = 5
	exitConflict        = 6 // includes stale claims and a used bootstrap
	exitUnavailable     = 7 // transport failure, timeout or busy storage
)

func exitCode(err error) int {
	switch comitia.FailureClass(err) {
	case "invalid":
		return exitUsage
	case "unauthenticated":
		return exitUnauthenticated
	case "forbidden":
		return exitForbidden
	case "not_found":
		return exitNotFound
	case "conflict", "stale_claim", "bootstrap_used":
		return exitConflict
	case "deadline_exceeded", "storage_busy", "storage_read_only", "storage_io", "storage_full":
		return exitUnavailable
	}
	var netErr net.Error
	// The HTTP client reports a non-JSON error status (a proxy's 502 or 503) as plain text.
	if errors.As(err, &netErr) || strings.HasPrefix(err.Error(), "http status 5") {
		return exitUnavailable
	}
	return exitInternal
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
		return errors.New("bootstrap is available only with embedded SQLite")
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

// remoteTimeout bounds each remote call; it stays above the longest server-side wait.
var remoteTimeout = comitia.MaxMessageWait + 10*time.Second

func open(cfg config) (comitia.API, func(), error) {
	if cfg.serverURL != "" {
		if u, e := url.Parse(cfg.serverURL); e == nil && strings.HasSuffix(strings.TrimRight(u.Path, "/"), "/v1") {
			return nil, func() {}, comitia.Invalidf("server URL must be the base URL without /v1 (for example http://127.0.0.1:8080)")
		}
		c, e := httpapi.NewClientWithInstanceDefaults(cfg.serverURL, &http.Client{Timeout: remoteTimeout}, cfg.instances)
		return c, func() {}, e
	}
	store, e := sqlite.OpenSQLite(cfg.db)
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

// parseFlags rejects leftovers: flag parsing stops at the first positional
// token, so an unquoted value would otherwise be truncated silently.
func parseFlags(fs *flag.FlagSet, args []string) error {
	if err := parseQuiet(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return comitia.Invalidf("unexpected argument %q; quote values that contain spaces", fs.Arg(0))
	}
	return nil
}

// parseQuiet prints usage only for --help, so a parse error leaves stderr with
// just the JSON error line.
func parseQuiet(fs *flag.FlagSet, args []string) error {
	out := fs.Output()
	var buf bytes.Buffer
	fs.SetOutput(&buf)
	err := fs.Parse(args)
	fs.SetOutput(out)
	if errors.Is(err, flag.ErrHelp) {
		_, _ = out.Write(buf.Bytes())
		return err
	}
	if err != nil {
		help := "comitia " + fs.Name() + " --help"
		if fs.Name() == "comitia" {
			help = "comitia --help"
		}
		return comitia.Invalidf("%v; see %s", err, help)
	}
	return nil
}

// requireFlags names the first missing required flag. Commands call it before
// their parse-only return, so the error precedes storage and network access.
func requireFlags(fs *flag.FlagSet, names ...string) error {
	for _, name := range names {
		if err := requireAny(fs, name); err != nil {
			return err
		}
	}
	return nil
}

// requireAny accepts a nonempty value for one of names; unset numeric flags
// count as missing even though their zero default prints as a value.
func requireAny(fs *flag.FlagSet, names ...string) error {
	for _, name := range names {
		f := fs.Lookup(name)
		if v := f.Value.String(); v != "" && (v != f.DefValue || flagProvided(fs, name)) {
			return nil
		}
	}
	return comitia.Invalidf("%s requires --%s", fs.Name(), strings.Join(names, " or --"))
}

const commandUsage = `Usage: comitia [global flags] <command> [flags]
Use comitia <command> --help for command flags.

Sessions and access:
  bootstrap         create the first local owner
  delegate          create a scoped child session
  revoke            revoke a session and descendants
  grant             grant context permissions to a session
  grant-revoke      revoke a grant
  credential-check  check effective context permissions
Contexts:
  workspace-create  create a workspace
  context-create    create a top-level or child context
  context-list      list visible contexts
  context-get       get one context
  context-children  list immediate child contexts
  context-archive   archive a context
  reference-add     attach a reference to a context
  reference-list    list a context's references
Messages and presence:
  send              append a deduplicated message
  replay            read messages after a sequence
  wait              wait for matching messages
  message-get       read one message in a readable context
  watch             print and acknowledge arriving messages
  ack, cursor       save or read an acknowledgement cursor
  participants      list sessions with context access
  instances         list recorded process activity
  instance-resume   save process resume metadata
Tasks:
  task-create       create shared work
  task-get          get one task
  task-list         list tasks in a context
  task-claim        claim a pending task
  task-recover      recover a lost token for your live claim
  task-renew        renew a claim
  task-complete     complete a claimed task
  task-release      release a claim
  task-fail         record permanent failure with a live claim
  task-cancel       cancel with a live claim or context-admin authority
Services:
  serve             run the SQLite HTTP daemon
  mcp               run an MCP stdio adapter
  mcp-http          run a remote MCP HTTP adapter
  server-info       inspect version and feature support
  storage-status    probe write readiness (administrator)
  version           print this binary's build identity
  help              show this usage

Global flags:
`

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
	case "send", "replay", "wait", "cursor", "ack", "watch", "instance-resume", "mcp", "mcp-http", "task-claim", "task-recover", "task-list":
		return true
	default:
		return false
	}
}

func nonNilList[T any](v []T) []T {
	if v == nil {
		return []T{}
	}
	return v
}
