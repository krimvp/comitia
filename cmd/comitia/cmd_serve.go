// The serving command group and the `serve` command: the SQLite HTTP daemon.
// runServe also routes `mcp` and `mcp-http`, whose bodies live in cmd_mcp.go;
// what the three share is in serving.go.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/httpapi"
)

func runServe(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	switch cmd {
	case "serve":
		return runServeDaemon(ctx, cfg, api, out, cmd, args)
	case "mcp":
		return runMCPStdio(ctx, cfg, api, out, cmd, args)
	case "mcp-http":
		return runMCPHTTP(ctx, cfg, api, out, cmd, args)
	default:
		return errUnknownCommand
	}
}

func runServeDaemon(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	listen, _, _, logRequests, err := serveFlags(args)
	if err != nil {
		return err
	}
	if cfg.parseOnly {
		return nil
	}
	if cfg.serverURL != "" {
		return comitia.Invalidf("serve requires embedded SQLite")
	}
	handler := httpapi.NewHandler(api)
	if logRequests {
		handler = serveLoggingHandler(handler)
	}
	ctx, stop := serveSignals(ctx, cmd)
	defer stop()
	return serveHTTP(ctx, listen, cfg.db, handler, endWaits(api))
}

func serveFlags(args []string) (string, int, int, bool, error) {
	fs := newFlags("serve")
	listen := fs.String("listen", env("COMITIA_LISTEN", "127.0.0.1:8080"), "listen address")
	logRequests := fs.Bool("log-requests", false, "log allowlisted method, route, status, duration and request_id (or COMITIA_HTTP_LOG)")
	depth := fs.Int("max-context-depth", 3, "maximum child nesting depth (top-level is 0)")
	count := fs.Int("max-child-contexts-per-grant", 20, "maximum children created by each grant (including archived children)")
	if err := parseFlags(fs, args); err != nil {
		return "", 0, 0, false, err
	}
	if *depth < 1 || *count < 1 {
		return "", 0, 0, false, fmt.Errorf("%w: context limits must be positive", comitia.ErrInvalid)
	}
	if err := booleanEnv(fs, "log-requests", "COMITIA_HTTP_LOG", logRequests); err != nil {
		return "", 0, 0, false, err
	}
	return *listen, *depth, *count, *logRequests, nil
}

func serveHTTP(ctx context.Context, address, db string, handler http.Handler, onShutdown ...func()) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	build := comitia.BuildIdentity()
	log.Printf("comitia serve listen=%q version=%q revision=%q db=%q", listener.Addr().String(), build.Version, build.Revision, db)
	return serveListener(ctx, listener, handler, onShutdown...)
}
