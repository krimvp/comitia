package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/mcp"
)

func runServe(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	switch cmd {
	case "serve":
		listen, _, _, err := serveFlags(args)
		if err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		if cfg.serverURL != "" {
			return comitia.Invalidf("serve requires embedded SQLite")
		}
		return http.ListenAndServe(listen, httpapi.NewHandler(api))
	case "mcp":
		fs := newFlags(cmd)
		dir := fs.String("sessions-dir", env("COMITIA_SESSIONS_DIR", ""), "private delegated credential directory (default ~/.local/share/comitia/sessions)")
		credentialURL := fs.String("credential-server-url", cfg.serverURL, "daemon URL stored in delegated credentials (required for embedded delegation)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		return (&mcp.Server{API: api, Credential: needCredential(cfg), SessionsDir: *dir, ServerURL: *credentialURL, InstanceDefaults: cfg.instances}).Serve(ctx, os.Stdin, os.Stdout)
	case "mcp-http":
		// Streamable HTTP transport for remote MCP clients. Unlike serve, this
		// adapter must run against a remote Comitia daemon: it authenticates each
		// caller with an Authorization header rather than a fixed process
		// credential, so it cannot use embedded SQLite.
		fs := newFlags(cmd)
		dir := fs.String("sessions-dir", env("COMITIA_SESSIONS_DIR", ""), "private delegated credential directory (default ~/.local/share/comitia/sessions)")
		allowDelegation := fs.Bool("allow-delegation", false, "offer delegate_session (credential files are written on this host)")
		listen := fs.String("listen", env("COMITIA_MCP_HTTP_LISTEN", "127.0.0.1:8391"), "listen address")
		origins := fs.String("allowed-origins", env("COMITIA_MCP_HTTP_ALLOWED_ORIGINS", ""), "comma-separated Origin allowlist (empty rejects any request carrying an Origin)")
		maxBody := fs.Int64("max-body-bytes", 1<<20, "maximum request body size")
		rate := fs.Float64("rate-per-second", 20, "per-credential request rate (0 disables)")
		burst := fs.Int("rate-burst", 60, "per-credential burst size (0 disables)")
		logRequests := fs.Bool("log-requests", false, "log allowlisted HTTP method, constant route, status and duration")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		if cfg.serverURL == "" {
			return comitia.Invalidf("mcp-http requires --server-url (or COMITIA_SERVER_URL)")
		}
		if cfg.credential != "" || os.Getenv("COMITIA_CREDENTIAL") != "" {
			return comitia.Invalidf("mcp-http takes credentials per request; unset COMITIA_CREDENTIAL")
		}
		if err := booleanEnv(fs, "allow-delegation", "COMITIA_MCP_HTTP_ALLOW_DELEGATION", allowDelegation); err != nil {
			return err
		}
		if err := booleanEnv(fs, "log-requests", "COMITIA_MCP_HTTP_LOG", logRequests); err != nil {
			return err
		}
		var allow []string
		for _, o := range strings.Split(*origins, ",") {
			if o = strings.TrimSpace(o); o != "" {
				allow = append(allow, o)
			}
		}
		handler := mcp.NewHTTPHandler(api, mcp.HTTPConfig{
			SessionsDir:      *dir,
			ServerURL:        cfg.serverURL,
			AllowDelegation:  *allowDelegation,
			AllowedOrigins:   allow,
			InstanceDefaults: cfg.instances,
			MaxBody:          *maxBody,
			RatePerSecond:    *rate,
			RateBurst:        *burst,
		})
		// Optional request logging uses only allowlisted, non-sensitive metadata.
		if *logRequests {
			handler = mcp.LoggingHandler(handler)
		}
		srv := &http.Server{
			Addr:              *listen,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      120 * time.Second,
			IdleTimeout:       120 * time.Second,
			MaxHeaderBytes:    16 << 10,
		}
		return mcp.ServeHTTP(ctx, srv)
	default:
		return errUnknownCommand
	}
}

func serveFlags(args []string) (string, int, int, error) {
	fs := newFlags("serve")
	listen := fs.String("listen", env("COMITIA_LISTEN", "127.0.0.1:8080"), "listen address")
	depth := fs.Int("max-context-depth", 3, "maximum child nesting depth (top-level is 0)")
	count := fs.Int("max-child-contexts-per-grant", 20, "maximum children created by each grant (including archived children)")
	if err := fs.Parse(args); err != nil {
		return "", 0, 0, err
	}
	if *depth < 1 || *count < 1 {
		return "", 0, 0, fmt.Errorf("%w: context limits must be positive", comitia.ErrInvalid)
	}
	return *listen, *depth, *count, nil
}

func booleanEnv(fs *flag.FlagSet, name, key string, value *bool) error {
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			explicit = true
		}
	})
	if explicit || os.Getenv(key) == "" {
		return nil
	}
	parsed, err := strconv.ParseBool(os.Getenv(key))
	if err != nil {
		return fmt.Errorf("%w: %s must be a boolean", comitia.ErrInvalid, key)
	}
	*value = parsed
	return nil
}
