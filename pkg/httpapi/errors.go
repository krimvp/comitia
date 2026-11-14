package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/krimvp/comitia/pkg/comitia"
)

// errorResponse is the error body: the public ErrorInfo and, for an internal
// failure whose storage class the server recognizes, that class as
// failure_class (a comitia.FailureClass value such as storage_busy). The
// status and the other members are unchanged by it. Every client decodes
// error bodies without rejecting unknown members, the frozen legacy client
// included, so older clients ignore it; an older server omits it.
type errorResponse struct {
	comitia.ErrorInfo
	FailureClass string `json:"failure_class,omitempty"`
}

// storageClasses maps each storage failure class that comitia.FailureClass
// derives from a SQLite primary result code to one such code.
var storageClasses = map[string]int{"storage_busy": 5, "storage_read_only": 8, "storage_io": 10, "storage_full": 13}

// storageError is a server's classified storage failure. Its Code is a SQLite
// primary result code of the class, so comitia.FailureClass gives the client
// the class the server saw; its text is that of any other remote error.
type storageError struct {
	message string
	code    int
}

func (e *storageError) Error() string { return e.message }
func (e *storageError) Code() int     { return e.code }

// remoteCause is the error behind a decoded error body.
func remoteCause(e errorResponse) error {
	if e.Code == "conflict" && e.Details != nil && e.Details.MessageID != "" {
		return &comitia.DedupeConflictError{MessageID: e.Details.MessageID, Sequence: e.Details.Sequence, Differs: e.Details.Differs}
	}
	cause := decodeError(e.Code, e.Message)
	if code, ok := storageClasses[e.FailureClass]; ok && e.Code == "internal" {
		return &storageError{message: cause.Error(), code: code}
	}
	return cause
}

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
	body := errorResponse{ErrorInfo: info}
	if info.Code == "internal" {
		body.Message = "internal server error"
		if class := comitia.FailureClass(err); storageClasses[class] != 0 {
			body.FailureClass = class
		}
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
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
