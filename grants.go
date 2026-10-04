package comitia

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
	Grants(string, string, time.Time) ([]Grant, error)
}
type GrantWriteStore interface {
	PutGrant(Grant) error
	RevokeGrantTree(string, time.Time) error
}

func (s *Service) RevokeGrant(ctx context.Context, raw, grantID string) error {
	return s.store.Update(ctx, func(w WriteStore) error {
		a, e := s.auth(w, raw)
		if e != nil {
			return e
		}
		if !a.principal.Admin {
			return ErrForbidden
		}
		return w.RevokeGrantTree(grantID, s.now().UTC())
	})
}
func (s *Service) GrantContext(ctx context.Context, raw string, in GrantInput) (Grant, error) {
	var g Grant
	e := s.store.Update(ctx, func(w WriteStore) error {
		a, e := s.auth(w, raw)
		if e != nil {
			return e
		}
		if _, e = w.Context(in.ContextID); e != nil {
			return e
		}
		target, e := w.Session(in.SessionID)
		if e != nil {
			return e
		}
		if target.ParentID != a.session.ID && !a.principal.Admin {
			return ErrForbidden
		}
		if in.Permissions == 0 || !in.ExpiresAt.After(s.now().UTC()) || in.ExpiresAt.After(target.ExpiresAt) || in.ExpiresAt.After(a.session.ExpiresAt) {
			return ErrInvalid
		}
		if a.principal.Admin {
			g = Grant{ID: id(), SessionID: in.SessionID, ContextID: in.ContextID, IssuerSessionID: a.session.ID, Permissions: in.Permissions, ExpiresAt: in.ExpiresAt, CreatedAt: s.now().UTC()}
			return w.PutGrant(g)
		}
		parents, e := w.Grants(a.session.ID, in.ContextID, s.now().UTC())
		if e != nil {
			return e
		}
		var source *Grant
		for i := range parents {
			p := &parents[i]
			if p.Permissions&(in.Permissions|PermissionGrant) == (in.Permissions|PermissionGrant) && !in.ExpiresAt.After(p.ExpiresAt) {
				source = p
				break
			}
		}
		if source == nil {
			return ErrForbidden
		}
		g = Grant{ID: id(), SessionID: in.SessionID, ContextID: in.ContextID, ParentGrantID: source.ID, IssuerSessionID: a.session.ID, Permissions: in.Permissions, ExpiresAt: in.ExpiresAt, CreatedAt: s.now().UTC()}
		return w.PutGrant(g)
	})
	return g, e
}
