package comitia

import (
	"context"
	"fmt"
)

// ArchiveContext is idempotent. Administrative status permits archiving, but never grants read access.
func (s *Service) ArchiveContext(ctx context.Context, raw, contextID string) error {
	return s.store.Update(ctx, func(w WriteStore) error {
		a, err := s.auth(w, raw)
		if err != nil {
			return err
		}
		if !a.principal.Admin {
			if _, _, err := s.require(w, raw, contextID, PermissionGrant); err != nil {
				return err
			}
		}
		if _, err := w.Context(contextID); err != nil {
			return err
		}
		return w.PutContextArchive(contextID, s.now().UTC())
	})
}

func (s *Service) contextMetadata(r ReadStore, c *WorkContext) error {
	at, err := r.ContextArchivedAt(c.ID, s.now().UTC())
	if err != nil {
		return err
	}
	c.ArchivedAt, c.Archived = at, at != nil
	return nil
}

func (s *Service) contextActive(r ReadStore, contextID string) error {
	at, err := r.ContextArchivedAt(contextID, s.now().UTC())
	if err != nil {
		return err
	}
	if at != nil {
		return fmt.Errorf("%w: context is archived", ErrInvalid)
	}
	return nil
}

func (s *Service) checkChildDepth(r ReadStore, parent WorkContext) error {
	seen := map[string]bool{}
	for depth := 1; ; depth++ {
		if depth > s.maxContextDepth {
			return fmt.Errorf("%w: maximum context nesting depth (%d) reached; top-level depth is 0", ErrInvalid, s.maxContextDepth)
		}
		if seen[parent.ID] {
			return fmt.Errorf("%w: context parent cycle", ErrInvalid)
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
