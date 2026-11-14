// The MCP adapters: `mcp` over stdio and `mcp-http` over streamable HTTP.
// runServe (cmd_serve.go) routes both commands here.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/mcp"
)

func runMCPStdio(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	fs := newFlags(cmd)
	dir := fs.String("sessions-dir", env("COMITIA_SESSIONS_DIR", ""), "private delegated credential directory (default ~/.local/share/comitia/sessions)")
	credentialURL := fs.String("credential-server-url", cfg.serverURL, "daemon URL stored in delegated credentials (required for embedded delegation)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if cfg.parseOnly {
		return nil
	}
	// A signal ends input instead of cancelling, so in-flight calls (with
	// long polls drained) still answer before the adapter exits.
	signalled, stop := serveSignals(ctx, cmd)
	defer stop()
	in, input := io.Pipe()
	go func() { _, err := io.Copy(input, os.Stdin); _ = input.CloseWithError(err) }()
	defer context.AfterFunc(signalled, func() { endWaits(api)(); _ = input.Close() })()
	return (&mcp.Server{API: api, Credential: needCredential(cfg), SessionsDir: *dir, ServerURL: *credentialURL, InstanceDefaults: cfg.instances}).Serve(ctx, in, os.Stdout)
}

func runMCPHTTP(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
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
	rate := fs.Float64("rate-per-second", 20, "per-credential request rate, also shared by unverified credentials (0 disables)")
	burst := fs.Int("rate-burst", 60, "per-credential burst size (0 disables)")
	logRequests := fs.Bool("log-requests", false, "log allowlisted HTTP method, constant route, status and duration")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if cfg.serverURL == "" {
		return comitia.Invalidf("mcp-http requires --server-url (or COMITIA_SERVER_URL)")
	}
	if cfg.parseOnly {
		return nil
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
		ReadHeaderTimeout: serveTimeouts.header,
		ReadTimeout:       serveTimeouts.read,
		WriteTimeout:      serveTimeouts.write,
		IdleTimeout:       serveTimeouts.idle,
		MaxHeaderBytes:    16 << 10,
	}
	srv.RegisterOnShutdown(endWaits(api))
	ctx, stop := serveSignals(ctx, cmd)
	defer stop()
	if err := mcp.ServeHTTP(ctx, srv); !errors.Is(err, context.Canceled) || ctx.Err() == nil {
		return err
	}
	return nil
}
