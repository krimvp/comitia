package comitia

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ListContexts returns readable contexts ordered by ID, strictly after afterID.
// The cursor is the last returned ID. An empty page marks the end. Administrative
// status alone does not grant visibility into contexts.
func (s *Service) ListContexts(ctx context.Context, raw, afterID string, limit int) ([]WorkContext, error) {
	if limit < 1 || limit > 1000 {
		return nil, Invalidf("limit must be between 1 and 1000")
	}
	if len(afterID) > 128 {
		return nil, Invalidf("after_id must be at most 128 bytes")
	}
	out := []WorkContext{}
	err := s.store.View(ctx, func(r ReadStore) error {
		a, err := s.auth(r, raw)
		if err != nil {
			return err
		}
		cursor := afterID
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			candidates, err := r.ContextCandidates(a.session.ID, cursor, 100)
			if err != nil {
				return err
			}
			access, err := contextGrants(r, a.session.ID, candidates, a.now)
			if err != nil {
				return err
			}
			start := len(out)
			for _, c := range candidates {
				cursor = c.ID
				if _, _, err := s.requireAuthenticated(access, a, c.ID, PermissionRead); err != nil {
					if errors.Is(err, ErrForbidden) {
						continue
					}
					return err
				}
				out = append(out, c)
				if len(out) == limit {
					break
				}
			}
			if err := s.contextMetadataBatch(r, out[start:], a.now); err != nil {
				return err
			}
			if len(out) == limit {
				return nil
			}

			if len(candidates) < 100 {
				return nil
			}
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) CreateWorkspace(ctx context.Context, raw, name string) (Workspace, error) {
	var x Workspace
	if strings.TrimSpace(name) == "" {
		return x, Invalidf("name required")
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		if _, e := s.admin(w, raw); e != nil {
			return e
		}
		x = Workspace{ID: id(), Name: name, CreatedAt: s.now().UTC()}
		return w.PutWorkspace(x)
	})
	return x, e
}

func (s *Service) CreateContext(ctx context.Context, raw string, in CreateContextInput) (WorkContext, error) {
	var x WorkContext
	if strings.TrimSpace(in.Name) == "" {
		return x, Invalidf("name required")
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		a, e := s.admin(w, raw)
		if e != nil {
			return e
		}
		if _, e = w.Workspace(in.WorkspaceID); e != nil {
			return e
		}
		x = WorkContext{ID: id(), WorkspaceID: in.WorkspaceID, Name: in.Name, CreatedAt: s.now().UTC()}
		if e = w.PutContext(x); e != nil {
			return e
		}
		return w.PutGrant(Grant{ID: id(), SessionID: a.session.ID, ContextID: x.ID, IssuerSessionID: a.session.ID, Permissions: PermissionAll, ExpiresAt: a.session.ExpiresAt, CreatedAt: s.now().UTC()})
	})
	return x, e
}

// childPermissions is what the creator of a child context holds on it.
const childPermissions = PermissionRead | PermissionWrite | PermissionClaim | PermissionGrant | PermissionCreate

// CreateChildContext requires create on the parent. The creator's grant on the child hangs off its parent grant
// and expires no later than it, so revoking the parent grant revokes the child grant and its delegates.
func (s *Service) CreateChildContext(ctx context.Context, raw string, in CreateChildContextInput) (WorkContext, error) {
	var x WorkContext
	if strings.TrimSpace(in.Name) == "" || in.ParentContextID == "" {
		return x, Invalidf("name and parent_context_id required")
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		a, gs, e := s.require(w, raw, in.ParentContextID, PermissionCreate)
		if e != nil {
			return e
		}
		parent, e := w.Context(in.ParentContextID)
		if e != nil {
			return e
		}
		if e := s.checkChildDepth(w, parent); e != nil {
			return e
		}
		if in.TaskID != "" {
			t, e := w.Task(in.TaskID)
			if errors.Is(e, ErrNotFound) || e == nil && t.ContextID != parent.ID {
				return fmt.Errorf("%w: task is not in the parent context", ErrInvalid)
			}
			if e != nil {
				return e
			}
		}
		var source *Grant
		for i := range gs {
			g := &gs[i]
			if g.Permissions&PermissionCreate == 0 {
				continue
			}
			count, e := w.ChildContextCount(g.ID)
			if e != nil {
				return e
			}
			if count < s.maxChildContextsPerGrant && (source == nil || g.ExpiresAt.After(source.ExpiresAt)) {
				source = g
			}
		}
		if source == nil {
			return fmt.Errorf("%w: maximum child contexts per creating grant (%d) reached", ErrInvalid, s.maxChildContextsPerGrant)
		}
		expires := source.ExpiresAt
		if a.session.ExpiresAt.Before(expires) {
			expires = a.session.ExpiresAt
		}
		now := s.now().UTC()
		x = WorkContext{ID: id(), WorkspaceID: parent.WorkspaceID, Name: in.Name, ParentContextID: parent.ID, TaskID: in.TaskID, CreatorSessionID: a.session.ID, ParentReadable: in.ParentReadable, CreatedAt: now}
		if e = w.PutContext(x); e != nil {
			return e
		}
		g := Grant{ID: id(), SessionID: a.session.ID, ContextID: x.ID, ParentGrantID: source.ID, IssuerSessionID: a.session.ID, Permissions: childPermissions, ExpiresAt: expires, CreatedAt: now}
		if e = w.PutGrant(g); e != nil {
			return e
		}
		return w.PutContextCreator(x.ID, g.ID, source.ID)
	})
	return x, e
}

// ListChildContexts returns metadata of the parent's children, ordered by ID, strictly after afterID. It needs read
// on the parent only; reading a child's messages and tasks still needs read on the child itself.
func (s *Service) ListChildContexts(ctx context.Context, raw, parentID, afterID string, limit int) ([]WorkContext, error) {
	if limit < 1 || limit > 1000 {
		return nil, Invalidf("limit must be between 1 and 1000")
	}
	if len(afterID) > 128 {
		return nil, Invalidf("after_id must be at most 128 bytes")
	}
	var out []WorkContext
	err := s.store.View(ctx, func(r ReadStore) error {
		a, _, err := s.require(r, raw, parentID, PermissionRead)
		if err != nil {
			return err
		}
		out, err = r.ChildContexts(parentID, afterID, limit)
		if err != nil {
			return err
		}
		return s.contextMetadataBatch(r, out, a.now)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) GetContext(ctx context.Context, raw, cid string) (WorkContext, error) {
	var x WorkContext
	e := s.store.View(ctx, func(r ReadStore) error {
		if _, _, e := s.require(r, raw, cid, PermissionRead); e != nil {
			return e
		}
		var e error
		x, e = r.Context(cid)
		if e != nil {
			return e
		}
		return s.contextMetadata(r, &x)
	})
	return x, e
}

func (s *Service) AddReference(ctx context.Context, raw string, in AddReferenceInput) (Reference, error) {
	var x Reference
	if e := validReference(in); e != nil {
		return x, e
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		if _, _, e := s.require(w, raw, in.ContextID, PermissionWrite); e != nil {
			return e
		}
		x = Reference{ID: id(), ContextID: in.ContextID, Kind: in.Kind, URI: in.URI, CreatedAt: s.now().UTC()}
		return w.PutReference(x)
	})
	return x, e
}
