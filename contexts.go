package comitia

import (
	"context"
	"errors"
)

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

// ContextCandidates only narrows discovery; Service rechecks effective read
// grants, including ancestry expiry and revocation, before returning any row.
func (x *sqlStore) ContextCandidates(sessionID, afterID string, limit int) ([]WorkContext, error) {
	rows, err := x.q.Query(`SELECT DISTINCT c.id,c.workspace_id,c.name,c.created_at
FROM contexts c JOIN grants g ON g.context_id=c.id
WHERE g.session_id=? AND c.id>? ORDER BY c.id LIMIT ?`, sessionID, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkContext{}
	for rows.Next() {
		var c WorkContext
		var created string
		if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.Name, &created); err != nil {
			return nil, err
		}
		c.CreatedAt = parse(created)
		out = append(out, c)
	}
	return out, rows.Err()
}
