package comitia

import (
	"context"
	"sync"
	"time"
)

const StorageProbeTimeout = 250 * time.Millisecond

type StorageStatus struct {
	WriteReady                 bool   `json:"write_ready"`
	ProbeDurationMS            int64  `json:"probe_duration_ms"`
	WriteAttempts              uint64 `json:"write_attempts"`
	WriteFailures              uint64 `json:"write_failures"`
	LastWriteFailureDurationMS int64  `json:"last_write_failure_duration_ms"`
	LastWriteDurationMS        int64  `json:"last_write_duration_ms"`
	MaxWriteDurationMS         int64  `json:"max_write_duration_ms"`
	LastWriteErrorClass        string `json:"last_write_error_class,omitempty"`
}

type StorageStatusAPI interface {
	StorageStatus(context.Context, string) (StorageStatus, error)
}

type observedStore struct {
	Store
	mu     sync.Mutex
	status StorageStatus
}

func (s *observedStore) Update(ctx context.Context, fn func(WriteStore) error) error {
	start := time.Now()
	err := s.Store.Update(ctx, fn)
	class := ""
	if err != nil {
		class = ErrorClass(err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.WriteAttempts++
	s.status.LastWriteDurationMS = time.Since(start).Milliseconds()
	if s.status.LastWriteDurationMS > s.status.MaxWriteDurationMS {
		s.status.MaxWriteDurationMS = s.status.LastWriteDurationMS
	}
	// Domain rejections do not imply unhealthy storage.
	if class == "internal" || class == "deadline_exceeded" || class == "cancelled" {
		s.status.WriteFailures++
		s.status.LastWriteFailureDurationMS = s.status.LastWriteDurationMS
		s.status.LastWriteErrorClass = FailureClass(err)
	} else if err == nil {
		s.status.LastWriteErrorClass = ""
	}
	return err
}

// StorageStatus is separate from capability discovery. The probe verifies writer
// admission/commit, not free disk space or the success of every future write.
// Global counters are visible only to an administrator and reset on restart.
func (s *Service) StorageStatus(ctx context.Context, raw string) (StorageStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, StorageProbeTimeout)
	defer cancel()
	if err := s.store.View(ctx, func(r ReadStore) error { _, err := s.admin(r, raw); return err }); err != nil {
		return StorageStatus{}, err
	}
	start := time.Now()
	err := s.store.Update(ctx, func(w WriteStore) error { _, err := s.admin(w, raw); return err })
	store := s.store.(*observedStore)
	store.mu.Lock()
	status := store.status
	store.mu.Unlock()
	status.WriteReady = err == nil
	status.ProbeDurationMS = time.Since(start).Milliseconds()
	if err != nil {
		class := ErrorClass(err)
		if class != "internal" && class != "deadline_exceeded" && class != "cancelled" {
			return StorageStatus{}, err
		}
	}
	return status, nil
}
