package service

import (
	"context"
	"errors"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

// maxDelegationDepth bounds new grant and session chains, which every later
// authorization walks. Deeper chains in existing databases still evaluate.
const maxDelegationDepth = 16

// delegationDepth counts parent links above id, stopping past the limit.
func delegationDepth(id string, parent func(string) (string, error)) (int, error) {
	n := 0
	for ; n < maxDelegationDepth; n++ {
		next, e := parent(id)
		if e != nil || next == "" {
			return n, e
		}
		id = next
	}
	return n, nil
}

// checkDelegationDepth refuses a new grant or session below parentID when its
// chain would exceed maxDelegationDepth links.
func checkDelegationDepth(kind, parentID string, parent func(string) (string, error)) error {
	n, e := delegationDepth(parentID, parent)
	if e != nil {
		return e
	}
	if n+1 > maxDelegationDepth {
		return model.Invalidf("%s delegation depth would exceed the maximum of %d", kind, maxDelegationDepth)
	}
	return nil
}

// delegationSource picks the first covering grant whose chain has room left.
func delegationSource(r model.GrantReadStore, parents []model.Grant, perms model.Permission, expires time.Time) (*model.Grant, error) {
	err := model.ErrForbidden
	for i := range parents {
		g := &parents[i]
		if g.Permissions&(perms|model.PermissionGrant) != perms|model.PermissionGrant || expires.After(g.ExpiresAt) {
			continue
		}
		if err = checkDelegationDepth("grant", g.ID, grantParent(r)); err == nil {
			return g, nil
		}
		if !errors.Is(err, model.ErrInvalid) {
			return nil, err
		}
	}
	return nil, err
}

func grantParent(r model.GrantReadStore) func(string) (string, error) {
	return func(id string) (string, error) { g, e := r.Grant(id); return g.ParentGrantID, e }
}

func sessionParent(r model.SessionReadStore) func(string) (string, error) {
	return func(id string) (string, error) { s, e := r.Session(id); return s.ParentID, e }
}

func (s *Service) RevokeGrant(ctx context.Context, raw, grantID string) error {
	return s.store.Update(ctx, func(w model.WriteStore) error {
		a, e := s.auth(w, raw)
		if e != nil {
			return e
		}
		g, e := w.Grant(grantID)
		if !a.principal.Admin && errors.Is(e, model.ErrNotFound) {
			return model.ErrForbidden
		}
		if e != nil {
			return e
		}
		if !a.principal.Admin && g.IssuerSessionID != a.session.ID {
			return model.ErrForbidden
		}
		return revokedAgain(w.RevokeGrantTree(grantID, s.now().UTC()), g.RevokedAt)
	})
}

func (s *Service) GrantContext(ctx context.Context, raw string, in model.GrantInput) (model.Grant, error) {
	var g model.Grant
	e := s.store.Update(ctx, func(w model.WriteStore) error {
		a, e := s.auth(w, raw)
		if e != nil {
			return e
		}
		var parents []model.Grant
		if !a.principal.Admin {
			var err error
			parents, err = w.Grants(a.session.ID, in.ContextID, s.now().UTC())
			if err != nil {
				return err
			}
			allowed := false
			for _, p := range parents {
				allowed = allowed || p.Permissions&model.PermissionGrant != 0
			}
			if !allowed {
				return model.ErrForbidden
			}
		}
		wc, e := w.Context(in.ContextID)
		if e != nil {
			if !a.principal.Admin && errors.Is(e, model.ErrNotFound) {
				return model.ErrForbidden
			}
			return e
		}
		target, e := w.Session(in.SessionID)
		if !a.principal.Admin && errors.Is(e, model.ErrNotFound) {
			return model.ErrForbidden
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
				return model.ErrForbidden
			}
		}
		if in.Permissions == 0 || in.Permissions&^model.PermissionAll != 0 || !in.ExpiresAt.After(s.now().UTC()) || in.ExpiresAt.After(target.ExpiresAt) || in.ExpiresAt.After(a.session.ExpiresAt) {
			return model.Invalidf("permissions must be nonzero known bits and expires_at must be within issuer and target session expiry")
		}
		if a.principal.Admin {
			if e = s.contextActive(w, in.ContextID); e != nil {
				return e
			}
			g = model.Grant{ID: model.NewID(), SessionID: in.SessionID, ContextID: in.ContextID, IssuerSessionID: a.session.ID, Permissions: in.Permissions, ExpiresAt: in.ExpiresAt, CreatedAt: s.now().UTC()}
			return w.PutGrant(g)
		}
		source, e := delegationSource(w, parents, in.Permissions, in.ExpiresAt)
		if e != nil {
			return e
		}
		if e = s.contextActive(w, in.ContextID); e != nil {
			return e
		}
		g = model.Grant{ID: model.NewID(), SessionID: in.SessionID, ContextID: in.ContextID, ParentGrantID: source.ID, IssuerSessionID: a.session.ID, Permissions: in.Permissions, ExpiresAt: in.ExpiresAt, CreatedAt: s.now().UTC()}
		return w.PutGrant(g)
	})
	return g, e
}

// Workspace eligibility uses only the target's own effective grants. Ancestor
// sessions may hold authority in workspaces their children do not belong to.
func sessionInWorkspace(r model.ReadStore, target model.Session, workspaceID string, now time.Time) (bool, error) {
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
