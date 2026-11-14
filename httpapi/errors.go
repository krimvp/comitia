package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/krimvp/comitia"
)

type errorResponse = comitia.ErrorInfo

func writeError(w http.ResponseWriter, err error, status int) {
	writeOperationError(w, err, status, "unknown", w.Header().Get("X-Request-ID"))
}
func writeOperationError(w http.ResponseWriter, err error, status int, operation, requestID string) {
	info := comitia.SafeError(operation, err, requestID)
	defaultStatus := http.StatusInternalServerError
	switch info.Code {
	case "unauthenticated":
		defaultStatus = http.StatusUnauthorized
	case "forbidden":
		defaultStatus = http.StatusForbidden
	case "not_found":
		defaultStatus = http.StatusNotFound
	case "conflict", "stale_claim", "bootstrap_used":
		defaultStatus = http.StatusConflict
	case "invalid":
		defaultStatus = http.StatusBadRequest
	case "deadline_exceeded", "cancelled":
		defaultStatus = http.StatusServiceUnavailable
	}
	if status == 0 {
		status = defaultStatus
	}
	if status == http.StatusMethodNotAllowed {
		info.Code = "method_not_allowed"
	}
	if info.Code == "internal" {
		info.Message = "internal server error"
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(info)
}

func decodeError(code, msg string) error {
	var base error
	switch code {
	case "unauthenticated":
		base = comitia.ErrUnauthenticated
	case "forbidden":
		base = comitia.ErrForbidden
	case "not_found":
		base = comitia.ErrNotFound
	case "conflict":
		base = comitia.ErrConflict
	case "invalid":
		base = comitia.ErrInvalid
	case "stale_claim":
		base = comitia.ErrStaleClaim
	case "bootstrap_used":
		base = comitia.ErrBootstrapUsed
	case "deadline_exceeded":
		base = context.DeadlineExceeded
	case "cancelled":
		base = context.Canceled
	default:
		return fmt.Errorf("remote error: %s", msg)
	}
	detail := strings.TrimPrefix(msg, base.Error()+": ")
	if detail == base.Error() {
		return base
	}
	return fmt.Errorf("%w: %s", base, detail)
}
