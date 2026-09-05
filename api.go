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
)
const PermissionAll = PermissionRead | PermissionWrite | PermissionClaim | PermissionGrant | PermissionAdmin

type Principal struct {
	ID, Name  string
	Admin     bool
	CreatedAt time.Time
}
type Session struct {
	ID, PrincipalID, ParentID string
	ExpiresAt                 time.Time
	RevokedAt                 *time.Time
	CreatedAt                 time.Time
}
type Credential struct {
	Session Session
	Token   string
}
type Workspace struct {
	ID, Name  string
	CreatedAt time.Time
}
type WorkContext struct {
	ID, WorkspaceID, Name string
	CreatedAt             time.Time
}
type Reference struct {
	ID, ContextID, Kind, URI string
	CreatedAt                time.Time
}
type Grant struct {
	ID, SessionID, ContextID, ParentGrantID, IssuerSessionID string
	Permissions                                              Permission
	ExpiresAt                                                time.Time
	RevokedAt                                                *time.Time
	CreatedAt                                                time.Time
}
type Message struct {
	ID, ContextID, SessionID, DedupeKey, Body string
	Sequence                                  int64
	CreatedAt                                 time.Time
}
type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskLeased    TaskStatus = "leased"
	TaskCompleted TaskStatus = "completed"
)

type Task struct {
	ID, ContextID, Title, Body string
	Status                     TaskStatus
	LeaseOwnerSessionID        string
	LeaseVersion               int64
	LeaseExpiresAt             *time.Time
	LeaseTokenHash             string `json:"-"`
	CreatedAt, UpdatedAt       time.Time
}
type TaskClaim struct {
	Task  Task
	Token string
}

type BootstrapInput struct {
	Credential, AdminName string
	SessionTTL            time.Duration
}
type CreateContextInput struct{ WorkspaceID, Name string }
type AddReferenceInput struct{ ContextID, Kind, URI string }
type GrantInput struct {
	SessionID, ContextID string
	Permissions          Permission
	ExpiresAt            time.Time
}
type DelegatedGrant struct {
	ContextID   string
	Permissions Permission
	ExpiresAt   time.Time
}
type DelegateInput struct {
	PrincipalName string
	TTL           time.Duration
	Grants        []DelegatedGrant
}
type MessageInput struct{ DedupeKey, Body string }
type TaskInput struct{ Title, Body string }

// API is the library-first application surface. Service and remote HTTP clients implement it.
type API interface {
	Bootstrap(context.Context, BootstrapInput) (Principal, Credential, error)
	CreatePrincipal(context.Context, string, string) (Principal, error)
	MintSession(context.Context, string, string, time.Duration) (Credential, error)
	DelegateSession(context.Context, string, DelegateInput) (Principal, Credential, []Grant, error)
	RevokeSession(context.Context, string, string) error
	RevokeGrant(context.Context, string, string) error
	CreateWorkspace(context.Context, string, string) (Workspace, error)
	CreateContext(context.Context, string, CreateContextInput) (WorkContext, error)
	GetContext(context.Context, string, string) (WorkContext, error)
	AddReference(context.Context, string, AddReferenceInput) (Reference, error)
	GrantContext(context.Context, string, GrantInput) (Grant, error)
	AppendMessage(context.Context, string, string, MessageInput) (Message, error)
	ReplayMessages(context.Context, string, string, int64, int) ([]Message, error)
	AckMessages(context.Context, string, string, int64) error
	MessageCursor(context.Context, string, string) (int64, error)
	CreateTask(context.Context, string, string, TaskInput) (Task, error)
	GetTask(context.Context, string, string) (Task, error)
	ListTasks(context.Context, string, string, TaskStatus, int) ([]Task, error)
	ClaimTask(context.Context, string, string, time.Duration) (TaskClaim, error)
	RenewClaim(context.Context, string, string, string, int64, time.Duration) (TaskClaim, error)
	CompleteTask(context.Context, string, string, string, int64) (Task, error)
}

type Clock func() time.Time
type Config struct {
	Store               Store
	Clock               Clock
	BootstrapCredential string
}

// Store supplies atomic persistence. Implementations must serialize concurrent Update calls.
type Store interface {
	View(context.Context, func(ReadStore) error) error
	Update(context.Context, func(WriteStore) error) error
	Close() error
}
type ReadStore interface {
	BootstrapDone() (bool, error)
	Principal(string) (Principal, error)
	Workspace(string) (Workspace, error)
	SessionByTokenHash(string) (Session, error)
	Session(string) (Session, error)
	Grants(string, string, time.Time) ([]Grant, error)
	Context(string) (WorkContext, error)
	MessageByDedupe(string, string, string) (Message, error)
	AckCursor(string, string) (int64, error)
	Messages(string, int64, int) ([]Message, error)
	Task(string) (Task, error)
	Tasks(string, TaskStatus, int) ([]Task, error)
}
type WriteStore interface {
	ReadStore
	MarkBootstrapDone() error
	PutPrincipal(Principal) error
	PutSession(Session, string) error
	PutWorkspace(Workspace) error
	PutContext(WorkContext) error
	PutReference(Reference) error
	PutGrant(Grant) error
	RevokeGrantTree(string, time.Time) error
	RevokeSessionTree(string, time.Time) error
	PutMessage(Message) (Message, error)
	Ack(string, string, int64, time.Time) error
	PutTask(Task) error
}
