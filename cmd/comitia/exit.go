// Process exit codes and the mapping from an error to one.

package main

import (
	"errors"
	"net"
	"strings"

	"github.com/krimvp/comitia"
)

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
	// A server's storage failure arrives classified (the HTTP client keeps its
	// failure_class), so FailureClass above maps it. Only an error body that is
	// not JSON at all, such as a proxy's 502 or 503 page, reaches this point as
	// the client's plain "http status N: malformed error response" text.
	if errors.As(err, &netErr) || strings.HasPrefix(err.Error(), "http status 5") {
		return exitUnavailable
	}
	return exitInternal
}
