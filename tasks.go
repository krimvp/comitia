package comitia

import (
	"context"
	"crypto/subtle"
	"time"
)

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskLeased    TaskStatus = "leased"
	TaskCompleted TaskStatus = "completed"
)

type Task struct {
	ID, ContextID, Title, Body string
	Status                     TaskStatus
	LeaseOwnerSessionID        string
	LeaseVersion               int64
	LeaseExpiresAt             *time.Time
	LeaseTokenHash             string `json:"-"`
	CreatedAt, UpdatedAt       time.Time
}
type TaskClaim struct {
	Task  Task
	Token string
}
type TaskInput struct{ Title, Body string }

type TaskAPI interface {
	CreateTask(context.Context, string, string, TaskInput) (Task, error)
	GetTask(context.Context, string, string) (Task, error)
	ListTasks(context.Context, string, string, TaskStatus, int) ([]Task, error)
	ClaimTask(context.Context, string, string, time.Duration) (TaskClaim, error)
	RenewClaim(context.Context, string, string, string, int64, time.Duration) (TaskClaim, error)
	CompleteTask(context.Context, string, string, string, int64) (Task, error)
}
type TaskReadStore interface {
	Task(string) (Task, error)
	Tasks(string, TaskStatus, int) ([]Task, error)
}
type TaskWriteStore interface {
	PutTask(Task) error
}

func (s *Service) CreateTask(ctx context.Context, raw, cid string, in TaskInput) (Task, error) {
	var t Task
	if in.Title == "" {
		return t, ErrInvalid
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		if _, _, e := s.require(w, raw, cid, PermissionWrite); e != nil {
			return e
		}
		now := s.now().UTC()
		t = Task{ID: id(), ContextID: cid, Title: in.Title, Body: in.Body, Status: TaskPending, CreatedAt: now, UpdatedAt: now}
		return w.PutTask(t)
	})
	return t, e
}
func (s *Service) GetTask(ctx context.Context, raw, id string) (Task, error) {
	var t Task
	e := s.store.View(ctx, func(r ReadStore) error {
		found, e := r.Task(id)
		if e != nil {
			return e
		}
		if _, _, e = s.require(r, raw, found.ContextID, PermissionRead); e != nil {
			return e
		}
		found.LeaseTokenHash = ""
		t = found
		return nil
	})
	if e != nil {
		return Task{}, e
	}
	return t, nil
}
func (s *Service) ListTasks(ctx context.Context, raw, cid string, status TaskStatus, limit int) ([]Task, error) {
	var out []Task
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalid
	}
	if status != "" && status != TaskPending && status != TaskLeased && status != TaskCompleted {
		return nil, ErrInvalid
	}
	e := s.store.View(ctx, func(r ReadStore) error {
		if _, _, e := s.require(r, raw, cid, PermissionRead); e != nil {
			return e
		}
		var e error
		out, e = r.Tasks(cid, status, limit)
		return e
	})
	return out, e
}
func (s *Service) ClaimTask(ctx context.Context, raw, taskID string, lease time.Duration) (TaskClaim, error) {
	var c TaskClaim
	if !validTTL(lease) {
		return c, ErrInvalid
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		t, e := w.Task(taskID)
		if e != nil {
			return e
		}
		a, _, e := s.require(w, raw, t.ContextID, PermissionClaim)
		if e != nil {
			return e
		}
		now := s.now().UTC()
		if t.Status == TaskCompleted {
			return ErrConflict
		}
		if t.LeaseExpiresAt != nil && t.LeaseExpiresAt.After(now) {
			return ErrConflict
		}
		t.Status = TaskLeased
		t.LeaseOwnerSessionID = a.session.ID
		t.LeaseVersion++
		exp := now.Add(lease)
		if exp.After(a.session.ExpiresAt) {
			exp = a.session.ExpiresAt
		}
		t.LeaseExpiresAt = &exp
		t.UpdatedAt = now
		c = TaskClaim{Task: t, Token: token()}
		t.LeaseTokenHash = tokenHash(c.Token)
		c.Task = t
		c.Task.LeaseTokenHash = ""
		return w.PutTask(t)
	})
	return c, e
}
func (s *Service) RenewClaim(ctx context.Context, raw, taskID, claimToken string, version int64, lease time.Duration) (TaskClaim, error) {
	return s.changeLease(ctx, raw, taskID, claimToken, version, lease, false)
}
func (s *Service) CompleteTask(ctx context.Context, raw, taskID, claimToken string, version int64) (Task, error) {
	c, e := s.changeLease(ctx, raw, taskID, claimToken, version, 0, true)
	return c.Task, e
}
func (s *Service) changeLease(ctx context.Context, raw, taskID, claimToken string, version int64, lease time.Duration, complete bool) (TaskClaim, error) {
	var c TaskClaim
	if !complete && !validTTL(lease) {
		return c, ErrInvalid
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		t, e := w.Task(taskID)
		if e != nil {
			return e
		}
		a, _, e := s.require(w, raw, t.ContextID, PermissionClaim)
		if e != nil {
			return e
		}
		now := s.now().UTC()
		if t.Status != TaskLeased || t.LeaseOwnerSessionID != a.session.ID || t.LeaseVersion != version || t.LeaseExpiresAt == nil || !t.LeaseExpiresAt.After(now) || subtle.ConstantTimeCompare([]byte(t.LeaseTokenHash), []byte(tokenHash(claimToken))) != 1 {
			return ErrStaleClaim
		}
		if complete {
			t.Status = TaskCompleted
			t.LeaseExpiresAt = nil
			t.LeaseTokenHash = ""
		} else {
			exp := now.Add(lease)
			if exp.After(a.session.ExpiresAt) {
				exp = a.session.ExpiresAt
			}
			t.LeaseExpiresAt = &exp
			t.LeaseVersion++
			c.Token = token()
			t.LeaseTokenHash = tokenHash(c.Token)
		}
		t.UpdatedAt = now
		c.Task = t
		c.Task.LeaseTokenHash = ""
		return w.PutTask(t)
	})
	return c, e
}
