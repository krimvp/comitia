package comitia

import (
	"context"
	"errors"
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
	RevokeGrantTree(string, time.Time) error
}

func (s *Service) RevokeGrant(ctx context.Context, raw, grantID string) error {
	return s.store.Update(ctx, func(w WriteStore) error {
		a, e := s.auth(w, raw)
		if e != nil {
			return e
		}
		if !a.principal.Admin {
			g, e := w.Grant(grantID)
			if errors.Is(e, ErrNotFound) {
				return ErrForbidden
			}
			if e != nil {
				return e
			}
			if g.IssuerSessionID != a.session.ID {
				return ErrForbidden
			}
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
		var parents []Grant
		if !a.principal.Admin {
			var err error
			parents, err = w.Grants(a.session.ID, in.ContextID, s.now().UTC())
			if err != nil {
				return err
			}
			allowed := false
			for _, p := range parents {
				allowed = allowed || p.Permissions&PermissionGrant != 0
			}
			if !allowed {
				return ErrForbidden
			}
		}
		wc, e := w.Context(in.ContextID)
		if e != nil {
			if !a.principal.Admin && errors.Is(e, ErrNotFound) {
				return ErrForbidden
			}
			return e
		}
		target, e := w.Session(in.SessionID)
		if !a.principal.Admin && errors.Is(e, ErrNotFound) {
			return ErrForbidden
		}
		if e != nil {
			return e
		}
		if target.ParentID != a.session.ID && !a.principal.Admin {
			ok, e := sessionInWorkspace(w, target, wc.WorkspaceID, s.now().UTC())
			if e != nil {
				return e
			}
			if !ok {
				return ErrForbidden
			}
		}
		if in.Permissions == 0 || in.Permissions&^PermissionAll != 0 || !in.ExpiresAt.After(s.now().UTC()) || in.ExpiresAt.After(target.ExpiresAt) || in.ExpiresAt.After(a.session.ExpiresAt) {
			return Invalidf("permissions must be nonzero known bits and expires_at must be within issuer and target session expiry")
		}
		if a.principal.Admin {
			if e = s.contextActive(w, in.ContextID); e != nil {
				return e
			}
			g = Grant{ID: id(), SessionID: in.SessionID, ContextID: in.ContextID, IssuerSessionID: a.session.ID, Permissions: in.Permissions, ExpiresAt: in.ExpiresAt, CreatedAt: s.now().UTC()}
			return w.PutGrant(g)
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
		if e = s.contextActive(w, in.ContextID); e != nil {
			return e
		}
		g = Grant{ID: id(), SessionID: in.SessionID, ContextID: in.ContextID, ParentGrantID: source.ID, IssuerSessionID: a.session.ID, Permissions: in.Permissions, ExpiresAt: in.ExpiresAt, CreatedAt: s.now().UTC()}
		return w.PutGrant(g)
	})
	return g, e
}

// Workspace eligibility uses only the target's own effective grants. Ancestor
// sessions may hold authority in workspaces their children do not belong to.
func sessionInWorkspace(r ReadStore, target Session, workspaceID string, now time.Time) (bool, error) {
	if target.RevokedAt != nil || !target.ExpiresAt.After(now) {
		return false, nil
	}
	cursor := ""
	for {
		candidates, e := r.ContextCandidates(target.ID, cursor, 100)
		if e != nil {
			return false, e
		}
		for _, c := range candidates {
			cursor = c.ID
			if c.WorkspaceID != workspaceID {
				continue
			}
			grants, e := r.Grants(target.ID, c.ID, now)
			if e != nil {
				return false, e
			}
			if len(grants) > 0 {
				return true, nil
			}
		}
		if len(candidates) < 100 {
			return false, nil
		}
	}
}
