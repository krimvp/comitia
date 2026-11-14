package comitia

import (
	"context"
)

func (s *Service) ServerInfo(ctx context.Context, raw string) (ServerInfo, error) {
	err := s.store.View(ctx, func(r ReadStore) error {
		_, err := s.auth(r, raw)
		return err
	})
	if err != nil {
		return ServerInfo{}, err
	}
	return ServerInfo{Build: BuildIdentity(), Version: BuildVersion(), ProtocolVersion: ProtocolVersion, Features: Features(), Limits: contentLimits()}, nil
}
