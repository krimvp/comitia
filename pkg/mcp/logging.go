// LoggingHandler: credential-safe access logging for the HTTP transport.

package mcp

import (
	"log"
	"net/http"
	"time"

	"github.com/krimvp/comitia/internal/httpserve"
)

// LoggingHandler records only allowlisted HTTP methods, a constant route,
// status and duration. Request URLs, headers and bodies may carry credentials
// or coordination content and must never enter logs.
func LoggingHandler(next http.Handler) http.Handler {
	return httpserve.Logged(next, func(q httpserve.Request) {
		log.Printf("mcp-http %s route=/mcp status=%d dur=%s", q.Method, q.Status, q.Elapsed.Round(time.Millisecond))
	})
}
