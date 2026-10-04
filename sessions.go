package comitia

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"
)

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
	RevokeSessionTree(string, time.Time) error
}

func (s *Service) Bootstrap(ctx context.Context, in BootstrapInput) (Principal, Credential, error) {
	var p Principal
	var c Credential
	if s.bootstrapCredential == "" || subtle.ConstantTimeCompare([]byte(in.Credential), []byte(s.bootstrapCredential)) != 1 {
		return p, c, ErrUnauthenticated
	}
	if strings.TrimSpace(in.AdminName) == "" || !validTTL(in.SessionTTL) {
		return p, c, Invalidf("admin_name required and session_ttl must be positive")
	}
	err := s.store.Update(ctx, func(w WriteStore) error {
		used, e := w.BootstrapDone()
		if e != nil {
			return e
		}
		if used {
			return ErrBootstrapUsed
		}
		now := s.now().UTC()
		p = Principal{ID: id(), Name: in.AdminName, Admin: true, CreatedAt: now}
		raw := token()
		c = Credential{Token: raw, Session: Session{ID: id(), PrincipalID: p.ID, ExpiresAt: now.Add(in.SessionTTL), CreatedAt: now}}
		if e = w.PutPrincipal(p); e != nil {
			return e
		}
		if e = w.PutSession(c.Session, tokenHash(raw)); e != nil {
			return e
		}
		return w.MarkBootstrapDone()
	})
	return p, c, err
}
func (s *Service) CreatePrincipal(ctx context.Context, raw, name string) (Principal, error) {
	var p Principal
	if strings.TrimSpace(name) == "" {
		return p, Invalidf("name required")
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		if _, e := s.admin(w, raw); e != nil {
			return e
		}
		p = Principal{ID: id(), Name: name, CreatedAt: s.now().UTC()}
		return w.PutPrincipal(p)
	})
	return p, e
}
func (s *Service) MintSession(ctx context.Context, raw, principalID string, ttl time.Duration) (Credential, error) {
	var c Credential
	if !validTTL(ttl) {
		return c, Invalidf("ttl must be positive")
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		if _, e := s.admin(w, raw); e != nil {
			return e
		}
		if _, e := w.Principal(principalID); e != nil {
			return e
		}
		now := s.now().UTC()
		t := token()
		c = Credential{Token: t, Session: Session{ID: id(), PrincipalID: principalID, ExpiresAt: now.Add(ttl), CreatedAt: now}}
		return w.PutSession(c.Session, tokenHash(t))
	})
	return c, e
}
func (s *Service) DelegateSession(ctx context.Context, raw string, in DelegateInput) (Principal, Credential, []Grant, error) {
	var p Principal
	var c Credential
	var out []Grant
	if strings.TrimSpace(in.PrincipalName) == "" || !validTTL(in.TTL) || len(in.Grants) == 0 {
		return p, c, nil, Invalidf("principal_name required, ttl must be positive and grants must not be empty")
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		parent, e := s.auth(w, raw)
		if e != nil {
			return e
		}
		now := s.now().UTC()
		expiry := now.Add(in.TTL)
		if expiry.After(parent.session.ExpiresAt) {
			return ErrForbidden
		}
		p = Principal{ID: id(), Name: in.PrincipalName, CreatedAt: now}
		tok := token()
		c = Credential{Token: tok, Session: Session{ID: id(), PrincipalID: p.ID, ParentID: parent.session.ID, ExpiresAt: expiry, CreatedAt: now}}
		for _, req := range in.Grants {
			if req.Permissions == 0 || req.ExpiresAt.After(expiry) || !req.ExpiresAt.After(now) {
				return Invalidf("grants permissions must be nonzero and expires_at must be within session expiry")
			}
			_, parents, e := s.require(w, raw, req.ContextID, PermissionGrant)
			if e != nil {
				return e
			}
			if e = s.contextActive(w, req.ContextID); e != nil {
				return e
			}
			var source *Grant
			for i := range parents {
				g := &parents[i]
				if g.Permissions&(req.Permissions|PermissionGrant) == (req.Permissions|PermissionGrant) && !req.ExpiresAt.After(g.ExpiresAt) {
					source = g
					break
				}
			}
			if source == nil {
				return ErrForbidden
			}
			out = append(out, Grant{ID: id(), SessionID: c.Session.ID, ContextID: req.ContextID, ParentGrantID: source.ID, IssuerSessionID: parent.session.ID, Permissions: req.Permissions, ExpiresAt: req.ExpiresAt, CreatedAt: now})
		}
		if e = w.PutPrincipal(p); e != nil {
			return e
		}
		if e = w.PutSession(c.Session, tokenHash(tok)); e != nil {
			return e
		}
		for _, g := range out {
			if e = w.PutGrant(g); e != nil {
				return e
			}
		}
		return nil
	})
	return p, c, out, e
}
func (s *Service) RevokeSession(ctx context.Context, raw, target string) error {
	return s.store.Update(ctx, func(w WriteStore) error {
		a, e := s.auth(w, raw)
		if e != nil {
			return e
		}
		t, e := w.Session(target)
		if !a.principal.Admin && errors.Is(e, ErrNotFound) {
			return ErrForbidden
		}
		if e != nil {
			return e
		}
		if !a.principal.Admin && t.ParentID != a.session.ID {
			return ErrForbidden
		}
		return w.RevokeSessionTree(target, s.now().UTC())
	})
}
