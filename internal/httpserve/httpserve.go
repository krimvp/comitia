package httpserve

import (
	"context"
	"net/http"
	"time"
)

// Request is what Logged reports about a finished request. It holds no URL,
// header value or body beyond what the caller reads from R itself.
type Request struct {
	R       *http.Request
	Header  http.Header // the response header
	Method  string      // r.Method if it is a standard method, else "other"
	Status  int
	Elapsed time.Duration
}

// Logged serves next and then calls record with the request's allowlisted
// method, response status and duration. Callers format the log line from
// constant labels only: request URLs, headers and bodies may carry
// credentials or coordination content.
func Logged(next http.Handler, record func(Request)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		record(Request{R: r, Header: rec.Header(), Method: method(r.Method), Status: rec.status, Elapsed: time.Since(started)})
	})
}

func method(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return m
	}
	return "other"
}

// statusRecorder keeps the first status written; a body written without a
// status is the implicit 200 it starts with.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.status = code
		s.wroteHeader = true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// Serve runs serve (srv.Serve or srv.ListenAndServe) until it returns or ctx
// is cancelled. If serve returns first, its error is err. On cancellation it
// shuts srv down, giving in-flight requests grace to finish, closes it, waits
// for serve to return and reports the shutdown error; err is then nil.
func Serve(ctx context.Context, srv *http.Server, serve func() error, grace time.Duration) (shutdownErr, err error) {
	served := make(chan error, 1)
	go func() { served <- serve() }()
	select {
	case err := <-served:
		return nil, err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	shutdownErr = srv.Shutdown(shutdownCtx)
	_ = srv.Close()
	<-served
	return shutdownErr, nil
}
