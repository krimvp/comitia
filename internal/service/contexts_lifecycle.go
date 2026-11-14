package service

import (
	"context"
	"fmt"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

// ArchiveContext is idempotent. Administrative status permits archiving, but never grants read access.
func (s *Service) ArchiveContext(ctx context.Context, raw, contextID string) error {
	return s.store.Update(ctx, func(w model.WriteStore) error {
		a, err := s.auth(w, raw)
		if err != nil {
			return err
		}
		if !a.principal.Admin {
			if _, _, err := s.requireAuthenticated(w, a, contextID, model.PermissionGrant); err != nil {
				return err
			}
		}
		if _, err := w.Context(contextID); err != nil {
			return err
		}
		return w.PutContextArchive(contextID, s.now().UTC())
	})
}

func (s *Service) contextMetadata(r model.ReadStore, c *model.WorkContext) error {
	return s.contextMetadataAt(r, c, s.now().UTC())
}

func (s *Service) contextMetadataAt(r model.ReadStore, c *model.WorkContext, now time.Time) error {
	at, err := r.ContextArchivedAt(c.ID, now)
	if err != nil {
		return err
	}
	c.ArchivedAt, c.Archived = at, at != nil
	return nil
}

func (s *Service) contextActive(r model.ReadStore, contextID string) error {
	return s.contextActiveAt(r, contextID, s.now().UTC())
}

func (s *Service) contextActiveAt(r model.ReadStore, contextID string, now time.Time) error {
	at, err := r.ContextArchivedAt(contextID, now)
	if err != nil {
		return err
	}
	if at != nil {
		return fmt.Errorf("%w: context is archived", model.ErrInvalid)
	}
	return nil
}

func (s *Service) checkChildDepth(r model.ReadStore, parent model.WorkContext) error {
	seen := map[string]bool{}
	for depth := 1; ; depth++ {
		if depth > s.maxContextDepth {
			return fmt.Errorf("%w: maximum context nesting depth (%d) reached; top-level depth is 0", model.ErrInvalid, s.maxContextDepth)
		}
		if seen[parent.ID] {
			return fmt.Errorf("%w: context parent cycle", model.ErrInvalid)
		}
		seen[parent.ID] = true
		if parent.ParentContextID == "" {
			return nil
		}
		var err error
		parent, err = r.Context(parent.ParentContextID)
		if err != nil {
			return err
		}
	}
}
