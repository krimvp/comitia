package comitia

import (
	"encoding/json"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

// Errors, permissions, content limits and the safe reporting of failures.

var (
	ErrNotFound        = model.ErrNotFound
	ErrUnauthenticated = model.ErrUnauthenticated
	ErrForbidden       = model.ErrForbidden
	ErrConflict        = model.ErrConflict
	ErrInvalid         = model.ErrInvalid
	ErrStaleClaim      = model.ErrStaleClaim
	ErrBootstrapUsed   = model.ErrBootstrapUsed
)

type Permission = model.Permission

const (
	PermissionRead  = model.PermissionRead
	PermissionWrite = model.PermissionWrite
	PermissionClaim = model.PermissionClaim
	PermissionGrant = model.PermissionGrant
	PermissionAdmin = model.PermissionAdmin

	// PermissionCreate allows creating child contexts under the context.
	PermissionCreate = model.PermissionCreate
)

const PermissionAll = model.PermissionAll

// ParsePermissions maps canonical permission names to their bits: read,
// write, claim, grant, admin and create, plus all for PermissionAll. Names
// are exact; repeats are allowed. An empty list is no permissions.
func ParsePermissions(names ...string) (Permission, error) {
	return model.ParsePermissions(names...)
}

// FormatPermissions returns the canonical names of the bits set in p, in bit
// order; it never returns all.
func FormatPermissions(p Permission) []string { return model.FormatPermissions(p) }

// ErrorInfo contains only public, safe recovery information.
type ErrorInfo = model.ErrorInfo

// ErrorDetails identifies the caller's own existing resource behind a conflict.
type ErrorDetails = model.ErrorDetails

// OperationalError preserves errors.Is classification across remote adapters.
type OperationalError = model.OperationalError

func ErrorClass(err error) string {
	return model.ErrorClass(err)
}

func NewRequestID() string {
	return model.NewRequestID()
}

func SafeError(operation string, err error, requestID string) ErrorInfo {
	return model.SafeError(operation, err, requestID)
}

// Diagnostic deliberately excludes errors, credentials, request bodies and
// client IDs.
type Diagnostic = model.Diagnostic

type DiagnosticHook = model.DiagnosticHook

func LogDiagnostic(d Diagnostic) {
	model.LogDiagnostic(d)
}

func ReportFailure(hook DiagnosticHook, operation string, err error, requestID string, duration time.Duration) {
	model.ReportFailure(hook, operation, err, requestID, duration)
}

// FailureClass recognizes safe SQLite primary codes without inspecting error text.
func FailureClass(err error) string {
	return model.FailureClass(err)
}

// SafeOperation maps untrusted route/tool names to fixed operational labels.
func SafeOperation(name string) string {
	return model.SafeOperation(name)
}

const (
	MaxMessageBodyBytes   = model.MaxMessageBodyBytes
	MaxTaskTitleBytes     = model.MaxTaskTitleBytes
	MaxTaskBodyBytes      = model.MaxTaskBodyBytes
	MaxReferenceKindBytes = model.MaxReferenceKindBytes
	MaxReferenceURIBytes  = model.MaxReferenceURIBytes
)

// ContentLimits advertises the write limits in server_info.
type ContentLimits = model.ContentLimits

// Invalidf names a failed field or rule while preserving ErrInvalid. Callers
// must use safe descriptions, never credentials or rejected input values.
func Invalidf(format string, args ...any) error {
	return model.Invalidf(format, args...)
}

// InvalidJSON describes decoder failures without exposing input values.
func InvalidJSON(err error, field string) error {
	return model.InvalidJSON(err, field)
}

// UnknownField lists accepted keys and suggests a nearby spelling. Names come
// from the contract; request values are never included.
func UnknownField(name string, fields []string) error {
	return model.UnknownField(name, fields)
}

// InvalidJSONFields adds the keys accepted at the failing object's level.
// InvalidJSON remains available to decoders without a request contract.
func InvalidJSONFields(err error, field string, raw json.RawMessage, target any) error {
	return model.InvalidJSONFields(err, field, raw, target)
}

// List limits: a limit must be between 1 and MaxListLimit, and adapters
// default an omitted limit to DefaultListLimit.
const (
	DefaultListLimit = model.DefaultListLimit
	MaxListLimit     = model.MaxListLimit
)

const (
	ListPageBytes = model.ListPageBytes
	MaxListBytes  = model.MaxListBytes
)
