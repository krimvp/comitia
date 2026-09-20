package mcp

import (
	"log"
	"net/http"
	"time"
)

// LoggingHandler wraps a handler with request logging suitable for an
// internet-facing endpoint. It records the HTTP method, the MCP protocol
// revision header, the MCP method, the status and the duration.
//
// It deliberately never records the Authorization header, the credential, or
// message bodies - the deployment's security model requires that credentials
// and coordination content stay out of logs.
func LoggingHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		// The MCP method travels in the Mcp-Method header on modern clients; the
		// path is the fallback identity for older ones.
		method := r.Header.Get("Mcp-Method")
		if method == "" {
			method = r.URL.Path
		}
		log.Printf("mcp-http %s method=%s proto=%q status=%d dur=%s remote=%s",
			r.Method, method, r.Header.Get("MCP-Protocol-Version"),
			rec.status, time.Since(start).Round(time.Millisecond), r.RemoteAddr)
	})
}

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
	if !s.wroteHeader {
		s.wroteHeader = true
	}
	return s.ResponseWriter.Write(b)
}
