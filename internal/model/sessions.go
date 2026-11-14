package model

import (
	"context"
	"time"
)

type Grant struct {
	ID, SessionID, ContextID, ParentGrantID, IssuerSessionID string
	Permissions                                              Permission
	ExpiresAt                                                time.Time
	RevokedAt                                                *time.Time
	CreatedAt                                                time.Time
}

type GrantInput struct {
	SessionID, ContextID string
	Permissions          Permission
	ExpiresAt            time.Time
}

type GrantAPI interface {
	RevokeGrant(context.Context, string, string) error
	GrantContext(context.Context, string, GrantInput) (Grant, error)
}

type GrantReadStore interface {
	// Grants returns only the session's own effective grants on this context at
	// now. Every grant ancestor and each ancestor's owner/issuer must exist, be
	// unrevoked and expire strictly after now. Missing ancestry/cycles exclude
	// candidates; other storage errors propagate. It does not inherit parent-read
	// access, apply context archival, or combine permissions from separate grants.
	Grants(string, string, time.Time) ([]Grant, error)
	// Grant returns the raw record, including expiry/revocation, or ErrNotFound.
	Grant(string) (Grant, error)
}

type GrantWriteStore interface {
	PutGrant(Grant) error
	// RevokeGrantTree durably revokes the grant and its descendants atomically.
	// It returns ErrNotFound only for a missing root; an already revoked root
	// succeeds, keeping existing revocation times.
	RevokeGrantTree(string, time.Time) error
}

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

type BootstrapInput struct {
	Credential, AdminName string
	SessionTTL            time.Duration
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

type SessionAPI interface {
	Bootstrap(context.Context, BootstrapInput) (Principal, Credential, error)
	CreatePrincipal(context.Context, string, string) (Principal, error)
	MintSession(context.Context, string, string, time.Duration) (Credential, error)
	DelegateSession(context.Context, string, DelegateInput) (Principal, Credential, []Grant, error)
	RevokeSession(context.Context, string, string) error
}

type SessionReadStore interface {
	BootstrapDone() (bool, error)
	Principal(string) (Principal, error)
	SessionByTokenHash(string) (Session, error)
	Session(string) (Session, error)
}

type SessionWriteStore interface {
	MarkBootstrapDone() error
	PutPrincipal(Principal) error
	PutSession(Session, string) error
	// RevokeSessionTree durably revokes the session and all child sessions atomically.
	// It returns ErrNotFound only for a missing root; an already revoked root
	// succeeds, keeping existing revocation times.
	RevokeSessionTree(string, time.Time) error
}
