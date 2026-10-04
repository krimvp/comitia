package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/mcp"
)

func runServe(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	switch cmd {
	case "serve":
		if cfg.serverURL != "" {
			return errors.New("serve requires embedded SQLite")
		}
		listen, _, _, err := serveFlags(args)
		if err != nil {
			return err
		}
		return http.ListenAndServe(listen, httpapi.NewHandler(api))
	case "mcp":
		if err := newFlags(cmd).Parse(args); err != nil {
			return err
		}
		return (&mcp.Server{API: api, Credential: needCredential(cfg), InstanceDefaults: cfg.instances}).Serve(ctx, os.Stdin, os.Stdout)
	case "mcp-http":
		// Streamable HTTP transport for remote MCP clients. Unlike serve, this
		// adapter must run against a remote Comitia daemon: it authenticates each
		// caller with an Authorization header rather than a fixed process
		// credential, so it cannot use embedded SQLite.
		if cfg.serverURL == "" {
			return errors.New("mcp-http requires --server-url (or COMITIA_SERVER_URL)")
		}
		if cfg.credential != "" || os.Getenv("COMITIA_CREDENTIAL") != "" {
			return errors.New("mcp-http takes credentials per request; unset COMITIA_CREDENTIAL")
		}
		fs := newFlags(cmd)
		listen := fs.String("listen", env("COMITIA_MCP_HTTP_LISTEN", "127.0.0.1:8391"), "listen address")
		origins := fs.String("allowed-origins", env("COMITIA_MCP_HTTP_ALLOWED_ORIGINS", ""), "comma-separated Origin allowlist (empty rejects any request carrying an Origin)")
		maxBody := fs.Int64("max-body-bytes", 1<<20, "maximum request body size")
		rate := fs.Float64("rate-per-second", 20, "per-credential request rate (0 disables)")
		burst := fs.Int("rate-burst", 60, "per-credential burst size (0 disables)")
		logRequests := fs.Bool("log-requests", env("COMITIA_MCP_HTTP_LOG", "") != "", "log request method, protocol header, status and duration")
		if err := fs.Parse(args); err != nil {
			return err
		}
		var allow []string
		for _, o := range strings.Split(*origins, ",") {
			if o = strings.TrimSpace(o); o != "" {
				allow = append(allow, o)
			}
		}
		handler := mcp.NewHTTPHandler(api, mcp.HTTPConfig{
			AllowedOrigins:    allow,
			InstanceDefaults:  cfg.instances,
			MaxBody:           *maxBody,
			RatePerSecond:     *rate,
			RateBurst:         *burst,
			ReadHeaderTimeout: 10 * time.Second,
		})
		// Optional request logging. Records method, protocol header, status and
		// duration only - never the Authorization header or message bodies.
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
