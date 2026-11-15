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

// errCredentialCheck answers a caller whose credential could not be looked up
// for a reason other than a bad credential. It is internal and carries no
// storage class: the caller is not known to be an administrator.
var errCredentialCheck = errors.New("storage status: credential check failed")

// StorageStatus is separate from capability discovery. The probe verifies writer
// admission and a rolled-back row write, not free disk space, commit durability
// or the success of every future write.
// Global counters are visible only to an administrator and reset on restart.
func (s *Service) StorageStatus(ctx context.Context, raw string) (model.StorageStatus, error) {
	// Authenticate under the caller's context, not the probe's budget: a lookup
	// cut by that budget would tell a valid administrator "unauthenticated"
	// exactly when storage is slow. Only a lookup that failed inside the View
	// gets errCredentialCheck; a failure of the View itself is returned
	// unchanged, as on every other operation.
	var lookupFailed bool
	if err := s.store.View(ctx, func(r model.ReadStore) error {
		_, err := s.storageAdmin(r, raw)
		lookupFailed = err != nil && !errors.Is(err, model.ErrUnauthenticated) && !errors.Is(err, model.ErrForbidden)
		return err
	}); err != nil {
		if !lookupFailed {
			return model.StorageStatus{}, err
		}
		if ctx.Err() != nil {
			return model.StorageStatus{}, ctx.Err()
		}
		return model.StorageStatus{}, errCredentialCheck
	}
	// start precedes the budget's deadline, so a probe cut by the budget
	// reports at least model.StorageProbeTimeout.
	start := time.Now()
	probe, cancel := context.WithTimeout(ctx, model.StorageProbeTimeout)
	defer cancel()
	store := s.store.(*observedStore)
	// An empty transaction commits even on a read-only database, so write a
	// row through the generic store interface and roll it back. The credential
	// is checked again inside the write; the caller is a known administrator by
	// now, so a lookup that fails there for a storage reason, such as the
	// budget, is a failed probe rather than a refusal.
	err := store.update(probe, func(w model.WriteStore) error {
		if _, err := s.storageAdmin(w, raw); err != nil {
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

// storageAdmin is admin through lookupCredential.
func (s *Service) storageAdmin(r model.ReadStore, raw string) (authInfo, error) {
	a, e := s.lookupCredential(r, raw)
	if e != nil {
		return a, e
	}
	if !a.principal.Admin {
		return a, model.ErrForbidden
	}
	return a, nil
}
