package comitia

import (
	"context"
)

// CheckPermissions checks local installer credentials without creating messages or grants.
func (s *Service) CheckPermissions(ctx context.Context, raw, contextID string, permissions Permission) error {
	if contextID == "" || permissions == 0 || permissions&^PermissionAll != 0 {
		return Invalidf("context_id and valid nonzero permissions required")
	}
	return s.store.View(ctx, func(r ReadStore) error {
		for bit := PermissionRead; bit <= PermissionCreate; bit <<= 1 {
			if permissions&bit != 0 {
				if _, _, err := s.require(r, raw, contextID, bit); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
