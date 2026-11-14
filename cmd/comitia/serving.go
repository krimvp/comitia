// What the serving commands (serve, mcp, mcp-http) share: shutdown on a
// signal, the long-poll drain, HTTP server timeouts, the serve-and-shut-down
// loop and serve's request logging.

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

	"github.com/krimvp/comitia"
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
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), serveShutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("comitia serve shutdown: %v", err)
	}
	_ = server.Close()
	<-served
	return nil
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

func serveLoggingHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		w.Header().Set("X-Request-ID", comitia.NewRequestID())
		rec := &serveStatusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		method := r.Method
		switch method {
		case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
			http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		default:
			method = "other"
		}
		route := "unknown"
		if strings.HasPrefix(r.URL.Path, "/v1/") {
			if operation := comitia.SafeOperation(strings.TrimPrefix(r.URL.Path, "/v1/")); operation != "unknown" {
				route = "/v1/" + operation
			}
		} else if strings.HasPrefix(r.URL.Path, "/ui/") {
			route = "/ui/"
		}
		requestID := rec.Header().Get("X-Request-ID")
		if decoded, err := hex.DecodeString(requestID); err != nil || len(decoded) != 16 {
			requestID = "unknown"
		}
		log.Printf("comitia http method=%s route=%s status=%d duration_ms=%d request_id=%s", method, route, rec.status, time.Since(started).Milliseconds(), requestID)
	})
}

type serveStatusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *serveStatusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *serveStatusRecorder) Write(b []byte) (int, error) {
	if !s.wroteHeader {
		s.WriteHeader(http.StatusOK)
	}
	return s.ResponseWriter.Write(b)
}
