package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

type observedStore struct {
	model.Store
	mu     sync.Mutex
	status model.StorageStatus
}

// errStorageProbe rolls back the probe's write so it leaves no trace.
var errStorageProbe = errors.New("storage probe rollback")

func (s *observedStore) Update(ctx context.Context, fn func(model.WriteStore) error) error {
	return s.update(ctx, fn, false)
}

func (s *observedStore) update(ctx context.Context, fn func(model.WriteStore) error, probe bool) error {
	start := time.Now()
	err := s.Store.Update(ctx, fn)
	if probe && errors.Is(err, errStorageProbe) {
		err = nil
	}
	class := ""
	if err != nil {
		class = model.ErrorClass(err)
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
		s.status.LastWriteErrorClass = model.FailureClass(err)
	} else if err == nil && !probe {
		// A probe does not show that real writes have recovered.
		s.status.LastWriteErrorClass = ""
	}
	return err
}

// StorageStatus is separate from capability discovery. The probe verifies writer
// admission and a rolled-back row write, not free disk space, commit durability
// or the success of every future write.
// Global counters are visible only to an administrator and reset on restart.
func (s *Service) StorageStatus(ctx context.Context, raw string) (model.StorageStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, model.StorageProbeTimeout)
	defer cancel()
	if err := s.store.View(ctx, func(r model.ReadStore) error { _, err := s.admin(r, raw); return err }); err != nil {
		return model.StorageStatus{}, err
	}
	start := time.Now()
	store := s.store.(*observedStore)
	// An empty transaction commits even on a read-only database, so write a
	// row through the generic store interface and roll it back.
	err := store.update(ctx, func(w model.WriteStore) error {
		if _, err := s.admin(w, raw); err != nil {
			return err
		}
		if err := w.PutPrincipal(model.Principal{ID: model.NewID(), Name: "storage-probe", CreatedAt: s.now().UTC()}); err != nil {
			return err
		}
		return errStorageProbe
	}, true)
	store.mu.Lock()
	status := store.status
	store.mu.Unlock()
	status.WriteReady = err == nil
	status.ProbeDurationMS = time.Since(start).Milliseconds()
	if err != nil {
		class := model.ErrorClass(err)
		if class != "internal" && class != "deadline_exceeded" && class != "cancelled" {
			return model.StorageStatus{}, err
		}
	}
	return status, nil
}
