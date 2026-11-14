package service

import (
	"context"
	"errors"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/schema"
)

func (s *Service) CreateTask(ctx context.Context, raw, cid string, in model.TaskInput) (model.Task, error) {
	var t model.Task
	eventContext := ""
	if e := validOperationID(in.OperationID); e != nil {
		return t, e
	}
	if in.Title == "" {
		return t, model.Invalidf("title required")
	}
	if e := model.ValidText("title", in.Title, model.MaxTaskTitleBytes); e != nil {
		return t, e
	}
	if e := model.ValidText("body", in.Body, model.MaxTaskBodyBytes); e != nil {
		return t, e
	}
	e := s.store.Update(ctx, func(w model.WriteStore) error {
		a, _, e := s.require(w, raw, cid, model.PermissionWrite)
		if e != nil {
			return e
		}
		fingerprint := taskFingerprint("create", cid, in.Title, in.Body)
		receipt, found, e := taskReceipt(w, a.session.ID, in.OperationID, fingerprint)
		if e != nil {
			return e
		}
		if found {
			t = receipt
			return nil
		}
		now := s.now().UTC()
		t = model.Task{ID: model.NewID(), ContextID: cid, Title: in.Title, Body: in.Body, Status: model.TaskPending, CreatedAt: now, UpdatedAt: now}
		if e := w.PutTask(t); e != nil {
			return e
		}
		if e := putTaskEvent(w, a.session.ID, t, "created", ""); e != nil {
			return e
		}
		eventContext = t.ContextID
		return putTaskReceipt(w, a.session.ID, in.OperationID, fingerprint, t)
	})
	if e != nil {
		return model.Task{}, e
	}
	s.notifyTaskEvent(eventContext)
	return t, e
}

func (s *Service) GetTask(ctx context.Context, raw, id string) (model.Task, error) {
	var t model.Task
	e := s.store.View(ctx, func(r model.ReadStore) error {
		found, e := s.privateTask(r, raw, id)
		if e != nil {
			return e
		}
		if _, _, e = s.require(r, raw, found.ContextID, model.PermissionRead); e != nil {
			return e
		}
		t, e = s.taskLeaseView(r, found, s.now().UTC())
		return e
	})
	if e != nil {
		return model.Task{}, e
	}
	return t, nil
}

func (s *Service) ListTasks(ctx context.Context, raw, cid string, status model.TaskStatus, limit int) ([]model.Task, error) {
	var out []model.Task
	if limit < 1 || limit > model.MaxListLimit {
		return nil, model.Invalidf("limit must be between 1 and %d", model.MaxListLimit)
	}
	if !schema.ValidTaskStatusFilter(status) {
		return nil, model.Invalidf("status must be pending, leased, completed, failed or cancelled")
	}
	e := s.store.View(ctx, func(r model.ReadStore) error {
		if _, _, e := s.require(r, raw, cid, model.PermissionRead); e != nil {
			return e
		}
		var e error
		out, e = r.Tasks(cid, status, limit)
		if e == nil {
			now := s.now().UTC()
			for i := range out {
				out[i], e = s.taskLeaseView(r, out[i], now)
				if e != nil {
					return e
				}
			}
		}
		return e
	})
	return checkTaskArray(out, e)
}

// holderTaskView returns a mutation result. Claim alone lets a session work a
// task it was given by ID, not read it: callers without read on the context get
// empty Title and Body, matching GetTask's refusal.
func (s *Service) holderTaskView(r model.ReadStore, a authInfo, t model.Task) (model.Task, error) {
	_, _, e := s.requireAuthenticated(r, a, t.ContextID, model.PermissionRead)
	if errors.Is(e, model.ErrForbidden) {
		t.Title, t.Body = "", ""
		return t, nil
	}
	return t, e
}

// Authenticate before consulting private IDs. Missing and unreadable tasks have
// the same public result, even for administrators without context access.
func (s *Service) privateTask(r model.ReadStore, raw, taskID string) (model.Task, error) {
	if _, err := s.auth(r, raw); err != nil {
		return model.Task{}, err
	}
	t, err := r.Task(taskID)
	if errors.Is(err, model.ErrNotFound) {
		return model.Task{}, model.ErrForbidden
	}
	return t, err
}
