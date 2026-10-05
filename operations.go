package comitia

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"time"
)

// ErrorInfo contains only public, safe recovery information.
type ErrorInfo struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Hint      string `json:"hint,omitempty"`
	RequestID string `json:"request_id,omitempty"`
}

// OperationalError preserves errors.Is classification across remote adapters.
type OperationalError struct {
	Info  ErrorInfo
	Cause error
}

func (e *OperationalError) Error() string { return e.Info.String() }
func (e *OperationalError) Unwrap() error { return e.Cause }

func ErrorClass(err error) string {
	for _, entry := range []struct {
		err  error
		code string
	}{
		{ErrUnauthenticated, "unauthenticated"}, {ErrForbidden, "forbidden"}, {ErrNotFound, "not_found"},
		{ErrConflict, "conflict"}, {ErrStaleClaim, "stale_claim"}, {ErrBootstrapUsed, "bootstrap_used"}, {ErrInvalid, "invalid"},
		{context.DeadlineExceeded, "deadline_exceeded"}, {context.Canceled, "cancelled"},
	} {
		if errors.Is(err, entry.err) {
			return entry.code
		}
	}
	return "internal"
}

func NewRequestID() string { return id() }

func SafeError(operation string, err error, requestID string) ErrorInfo {
	var remote *OperationalError
	if errors.As(err, &remote) && len(remote.Info.RequestID) == 32 {
		if _, e := hex.DecodeString(remote.Info.RequestID); e == nil {
			requestID = remote.Info.RequestID
		}
	}
	info := ErrorInfo{Code: ErrorClass(err), RequestID: requestID}
	switch info.Code {
	case "invalid":
		info.Message = err.Error()
	case "unauthenticated":
		info.Message = ErrUnauthenticated.Error()
	case "forbidden":
		info.Message = ErrForbidden.Error()
	case "not_found":
		info.Message = ErrNotFound.Error()
	case "bootstrap_used":
		info.Message = ErrBootstrapUsed.Error()
	case "conflict":
		info.Message = ErrConflict.Error()
		switch operation {
		case "send_message":
			info.Hint = "Reuse the original message with this dedupe key, or use a new key for a different message."
		case "claim_task":
			info.Hint = "Call get_task to refresh task status; claim only a pending task or one whose lease has expired."
		case "release_task":
			info.Hint = "Call get_task to refresh task status before releasing a current lease."
		default:
			info.Hint = "Refresh the resource state before retrying this operation."
		}
	case "stale_claim":
		info.Message = ErrStaleClaim.Error()
		info.Hint = "Call get_task to refresh lease state. Use the Token and Task.LeaseVersion from the latest claim or renewal together; reclaim an expired lease before changing it."
	default:
		info.Message = "internal error"
		info.Hint = "Ask the operator to check storage_status and report the request_id. Before retrying a write, read its state or reuse its dedupe key; the outcome may be uncertain."
		if operation == "replay_messages" || operation == "wait_messages" {
			info.Hint = "Retry with a smaller replay page or byte budget. If the failure persists, report the request_id to the operator."
		}
	}
	return info
}

// Diagnostic deliberately excludes errors, credentials, request bodies and client IDs.
// Hooks may be called concurrently.
type Diagnostic struct {
	Operation  string
	ErrorClass string
	RequestID  string
	Duration   time.Duration
	Build      BuildInfo
}
type DiagnosticHook func(Diagnostic)

func LogDiagnostic(d Diagnostic) {
	log.Printf("comitia operation=%s error_class=%s request_id=%s duration_ms=%d version=%s revision=%s dirty=%s", d.Operation, d.ErrorClass, d.RequestID, d.Duration.Milliseconds(), d.Build.Version, d.Build.Revision, d.Build.Dirty)
}

func ReportFailure(hook DiagnosticHook, operation string, err error, requestID string, duration time.Duration) {
	class := ErrorClass(err)
	if class != "internal" && class != "deadline_exceeded" && class != "cancelled" {
		return
	}
	if hook == nil {
		hook = LogDiagnostic
	}
	hook(Diagnostic{Operation: SafeOperation(operation), ErrorClass: FailureClass(err), RequestID: requestID, Duration: duration, Build: BuildIdentity()})
}

// FailureClass recognizes safe SQLite primary codes without inspecting error text.
func FailureClass(err error) string {
	class := ErrorClass(err)
	if class != "internal" {
		return class
	}
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		switch coded.Code() & 0xff {
		case 5, 6:
			return "storage_busy"
		case 8:
			return "storage_read_only"
		case 10:
			return "storage_io"
		case 13:
			return "storage_full"
		}
	}
	return class
}

var operationNames = map[string]string{
	"messages/schemas/list": "list_message_schemas", "messages/schemas/get": "get_message_schema",
	"messages/append": "send_message", "messages/replay": "replay_messages", "messages/wait": "wait_messages", "messages/ack": "ack_messages", "messages/cursor": "message_cursor", "participants/list": "list_participants",
	"tasks/create": "create_task", "tasks/get": "get_task", "tasks/list": "list_tasks", "tasks/claim": "claim_task", "tasks/renew": "renew_task", "tasks/complete": "complete_task", "tasks/release": "release_task",
	"instances/list": "list_instances", "instances/resume": "set_instance_resume",
	"contexts/list": "list_contexts", "workspaces/create": "create_workspace", "contexts/create": "create_context", "contexts/create-child": "create_context", "contexts/children": "list_child_contexts", "contexts/get": "get_context", "references/add": "add_reference", "contexts/archive": "archive_context",
	"principals/create": "create_principal", "sessions/mint": "mint_session", "sessions/delegate": "delegate_session", "sessions/revoke": "revoke_session", "grants/create": "grant_context", "grants/revoke": "revoke_grant",
}

// SafeOperation maps untrusted route/tool names to fixed operational labels.
func SafeOperation(name string) string {
	if name == "server_info" || name == "storage_status" {
		return name
	}
	if operation, ok := operationNames[name]; ok {
		return operation
	}
	for _, operation := range operationNames {
		if name == operation {
			return operation
		}
	}
	return "unknown"
}

func (e ErrorInfo) String() string {
	if e.Hint == "" {
		return e.Message
	}
	return fmt.Sprintf("%s. %s (request_id: %s)", e.Message, e.Hint, e.RequestID)
}
