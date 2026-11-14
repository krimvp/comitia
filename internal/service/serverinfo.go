package service

import (
	"context"

	"github.com/krimvp/comitia/internal/model"
)

func (s *Service) ServerInfo(ctx context.Context, raw string) (model.ServerInfo, error) {
	err := s.store.View(ctx, func(r model.ReadStore) error {
		_, err := s.auth(r, raw)
		return err
	})
	if err != nil {
		return model.ServerInfo{}, err
	}
	return model.ServerInfo{Build: model.BuildIdentity(), Version: model.BuildVersion(), ProtocolVersion: model.ProtocolVersion, Features: model.Features(), Limits: model.DefaultContentLimits()}, nil
}
