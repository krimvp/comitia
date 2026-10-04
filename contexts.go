package comitia

import (
	"context"
	"errors"
	"strings"
	"time"
)

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
type CreateContextInput struct{ WorkspaceID, Name string }
type AddReferenceInput struct{ ContextID, Kind, URI string }

type ContextAPI interface {
	ListContexts(context.Context, string, string, int) ([]WorkContext, error)
	CreateWorkspace(context.Context, string, string) (Workspace, error)
	CreateContext(context.Context, string, CreateContextInput) (WorkContext, error)
	GetContext(context.Context, string, string) (WorkContext, error)
	AddReference(context.Context, string, AddReferenceInput) (Reference, error)
}
type ContextReadStore interface {
	ContextCandidates(string, string, int) ([]WorkContext, error)
	Workspace(string) (Workspace, error)
	Context(string) (WorkContext, error)
}
type ContextWriteStore interface {
	PutWorkspace(Workspace) error
	PutContext(WorkContext) error
	PutReference(Reference) error
}

// ListContexts returns readable contexts ordered by ID, strictly after afterID.
// The cursor is the last returned ID. An empty page marks the end. Administrative
// status alone does not grant visibility into contexts.
func (s *Service) ListContexts(ctx context.Context, raw, afterID string, limit int) ([]WorkContext, error) {
	if limit < 1 || limit > 1000 || len(afterID) > 128 {
		return nil, ErrInvalid
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
			for _, c := range candidates {
				cursor = c.ID
				if _, _, err := s.require(r, raw, c.ID, PermissionRead); err != nil {
					if errors.Is(err, ErrForbidden) {
						continue
					}
					return err
				}
				out = append(out, c)
				if len(out) == limit {
					return nil
				}
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
		return x, ErrInvalid
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
		return x, ErrInvalid
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
func (s *Service) GetContext(ctx context.Context, raw, cid string) (WorkContext, error) {
	var x WorkContext
	e := s.store.View(ctx, func(r ReadStore) error {
		if _, _, e := s.require(r, raw, cid, PermissionRead); e != nil {
			return e
		}
		var e error
		x, e = r.Context(cid)
		return e
	})
	return x, e
}
func (s *Service) AddReference(ctx context.Context, raw string, in AddReferenceInput) (Reference, error) {
	var x Reference
	if in.Kind == "" || in.URI == "" {
		return x, ErrInvalid
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
