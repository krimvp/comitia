// Package comitia implements the application rules for durable, delegated agent work.
package comitia

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrForbidden       = errors.New("forbidden")
	ErrConflict        = errors.New("conflict")
	ErrInvalid         = errors.New("invalid input")
	ErrStaleClaim      = errors.New("stale task claim")
	ErrBootstrapUsed   = errors.New("bootstrap already completed")
)

type Permission uint32

const (
	PermissionRead Permission = 1 << iota
	PermissionWrite
	PermissionClaim
	PermissionGrant
	PermissionAdmin
	// PermissionCreate allows creating child contexts under the context.
	PermissionCreate
)
const PermissionAll = PermissionRead | PermissionWrite | PermissionClaim | PermissionGrant | PermissionAdmin | PermissionCreate

// API is the library-first application surface. Service and remote HTTP clients implement it.
type API interface {
	ServerInfoAPI
	SessionAPI
	ContextAPI
	GrantAPI
	MessageAPI
	TaskAPI
	PresenceAPI
}

type Clock func() time.Time
type Config struct {
	Store               Store
	Clock               Clock
	BootstrapCredential string
	MessageSchemas      map[string]PayloadValidator
	// Top-level contexts have depth 0. Zero values use defaults (3 and 20).
	MaxContextDepth          int
	MaxChildContextsPerGrant int
}

// Store supplies atomic persistence. Each View observes one consistent snapshot;
// each Update serializes with other Updates, reads its own writes and commits
// atomically only when its callback returns nil. Errors and panics roll back and
// release transaction resources. Cancellation covers admission and SQL work.
// Callback stores must not escape the callback. Authorization caches belong to
// that transaction and must be invalidated by grant/session mutations.
type Store interface {
	View(context.Context, func(ReadStore) error) error
	Update(context.Context, func(WriteStore) error) error
	Close() error
}
type ReadStore interface {
	SessionReadStore
	ContextReadStore
	GrantReadStore
	MessageReadStore
	TaskReadStore
	PresenceReadStore
}
type WriteStore interface {
	ReadStore
	SessionWriteStore
	ContextWriteStore
	GrantWriteStore
	MessageWriteStore
	TaskWriteStore
	PresenceWriteStore
}
