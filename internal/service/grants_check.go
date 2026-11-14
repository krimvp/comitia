package service

import (
	"context"

	"github.com/krimvp/comitia/internal/model"
)

// CheckPermissions checks local installer credentials without creating messages or grants.
func (s *Service) CheckPermissions(ctx context.Context, raw, contextID string, permissions model.Permission) error {
	if contextID == "" || permissions == 0 || permissions&^model.PermissionAll != 0 {
		return model.Invalidf("context_id and valid nonzero permissions required")
	}
	return s.store.View(ctx, func(r model.ReadStore) error {
		for bit := model.PermissionRead; bit <= model.PermissionCreate; bit <<= 1 {
			if permissions&bit != 0 {
				if _, _, err := s.require(r, raw, contextID, bit); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
