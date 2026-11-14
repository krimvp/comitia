// What the serving commands (serve, mcp, mcp-http) share: shutdown on a
// signal, the long-poll drain, HTTP server timeouts, serve's listener and
// serve's request logging. The serve-and-shut-down loop and the logging
// wrapper themselves live in internal/httpserve, shared with package mcp.

package main

import (
	"context"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/krimvp/comitia/internal/httpserve"
	"github.com/krimvp/comitia/pkg/comitia"
)

// serveTimeouts bound unauthenticated slow clients. Writes may outlast the
// longest messages/wait long poll; reads end once the body is consumed.
var serveTimeouts = struct{ header, read, write, idle time.Duration }{10 * time.Second, 30 * time.Second, comitia.MaxMessageWait + time.Minute, 120 * time.Second}

// serveShutdownTimeout bounds how long in-flight requests may finish after
// cancellation; long polls are drained by onShutdown rather than waited out.
const serveShutdownTimeout = 10 * time.Second

func serveListener(ctx context.Context, listener net.Listener, handler http.Handler, onShutdown ...func()) error {
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: serveTimeouts.header,
		ReadTimeout:       serveTimeouts.read,
		WriteTimeout:      serveTimeouts.write,
		IdleTimeout:       serveTimeouts.idle,
		MaxHeaderBytes:    16 << 10,
	}
	for _, f := range onShutdown {
		server.RegisterOnShutdown(f)
	}
	shutdownErr, err := httpserve.Serve(ctx, server, func() error { return server.Serve(listener) }, serveShutdownTimeout)
	if shutdownErr != nil {
		log.Printf("comitia serve shutdown: %v", shutdownErr)
	}
	return err
}

// serveSignals cancels on SIGINT or SIGTERM so a service drains long polls,
// shuts down and closes storage before exiting successfully.
func serveSignals(ctx context.Context, cmd string) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case s := <-signals:
			log.Printf("comitia %s stopping signal=%s", cmd, s)
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, func() { signal.Stop(signals); cancel() }
}

// endWaits returns the API's long-poll drain, or a no-op.
func endWaits(api comitia.API) func() {
	if d, ok := api.(interface{ EndWaits() }); ok {
		return d.EndWaits
	}
	return func() {}
}

// serveLoggingHandler gives each request an X-Request-ID (the API handler may
// replace it with the ID of an error it reports) and logs the allowlisted
// method, the operation route, the status, the duration and that ID.
func serveLoggingHandler(next http.Handler) http.Handler {
	logged := httpserve.Logged(next, func(q httpserve.Request) {
		route := "unknown"
		if strings.HasPrefix(q.R.URL.Path, "/v1/") {
			if operation := comitia.SafeOperation(strings.TrimPrefix(q.R.URL.Path, "/v1/")); operation != "unknown" {
				route = "/v1/" + operation
			}
		} else if strings.HasPrefix(q.R.URL.Path, "/ui/") {
			route = "/ui/"
		}
		requestID := q.Header.Get("X-Request-ID")
		if decoded, err := hex.DecodeString(requestID); err != nil || len(decoded) != 16 {
			requestID = "unknown"
		}
		log.Printf("comitia http method=%s route=%s status=%d duration_ms=%d request_id=%s", q.Method, route, q.Status, q.Elapsed.Milliseconds(), requestID)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", comitia.NewRequestID())
		logged.ServeHTTP(w, r)
	})
}
