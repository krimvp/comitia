package comitia

import (
	"context"
	"crypto/subtle"
	"errors"
	"math"
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
	LeaseOwnerInstance         *AgentInstance `json:"lease_owner_instance,omitempty"`
}
type TaskClaim struct {
	Task  Task
	Token string
}
type TaskInput struct{ Title, Body string }

// OpenLease asks for a lease that lasts until the task is completed or released.
// Every lease is capped at the claiming session's expiry, so it is still bounded.
const OpenLease = time.Duration(math.MaxInt64)

type TaskAPI interface {
	CreateTask(context.Context, string, string, TaskInput) (Task, error)
	GetTask(context.Context, string, string) (Task, error)
	ListTasks(context.Context, string, string, TaskStatus, int) ([]Task, error)
	ClaimTask(context.Context, string, string, time.Duration, ...AgentInstance) (TaskClaim, error)
	RenewClaim(context.Context, string, string, string, int64, time.Duration) (TaskClaim, error)
	CompleteTask(context.Context, string, string, string, int64) (Task, error)
	ReleaseTask(context.Context, string, string, string, int64) (Task, error)
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

// The optional instance records which process of a shared session holds the
// claim. It is self-reported routing metadata and confers no permissions.
func (s *Service) ClaimTask(ctx context.Context, raw, taskID string, lease time.Duration, instance ...AgentInstance) (TaskClaim, error) {
	var c TaskClaim
	if !validTTL(lease) || len(instance) > 1 {
		return c, ErrInvalid
	}
	var owner *AgentInstance
	if len(instance) == 1 {
		var e error
		if owner, e = normalizeAgentInstance(&instance[0]); e != nil {
			return c, e
		}
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
			live, e := s.holderCanClaim(w, t, now)
			if e != nil {
				return e
			}
			if live {
				return ErrConflict
			}
		}
		t.Status = TaskLeased
		t.LeaseOwnerSessionID = a.session.ID
		t.LeaseOwnerInstance = owner
		t.LeaseVersion++
		exp := leaseExpiry(now, lease, a.session)
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
			exp := leaseExpiry(now, lease, a.session)
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

// A lease whose holder session was revoked, expired or lost claim on the
// context can never be renewed or completed, so it counts as expired.
func (s *Service) holderCanClaim(r ReadStore, t Task, now time.Time) (bool, error) {
	holder, e := r.Session(t.LeaseOwnerSessionID)
	if errors.Is(e, ErrNotFound) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if holder.RevokedAt != nil || !holder.ExpiresAt.After(now) {
		return false, nil
	}
	grants, e := r.Grants(holder.ID, t.ContextID, now)
	if e != nil {
		return false, e
	}
	for _, g := range grants {
		if g.Permissions&PermissionClaim != 0 {
			return true, nil
		}
	}
	return false, nil
}

// Leases never outlive the session that holds them, which also bounds OpenLease.
func leaseExpiry(now time.Time, lease time.Duration, holder Session) time.Time {
	if lease >= holder.ExpiresAt.Sub(now) {
		return holder.ExpiresAt
	}
	return now.Add(lease)
}

// ReleaseTask returns a leased task to pending. The holder releases with its
// claim token; a context admin may release without it, e.g. to free a task whose
// worker went silent, but must name the current lease version. Releasing an
// expired lease is allowed and just tidies the task.
func (s *Service) ReleaseTask(ctx context.Context, raw, taskID, claimToken string, version int64) (Task, error) {
	var t Task
	e := s.store.Update(ctx, func(w WriteStore) error {
		found, e := w.Task(taskID)
		if e != nil {
			return e
		}
		a, _, e := s.require(w, raw, found.ContextID, PermissionClaim)
		holder := e == nil && found.LeaseOwnerSessionID == a.session.ID && claimToken != "" && subtle.ConstantTimeCompare([]byte(found.LeaseTokenHash), []byte(tokenHash(claimToken))) == 1
		if !holder {
			if _, _, e = s.require(w, raw, found.ContextID, PermissionAdmin); e != nil {
				return e
			}
		}
		now := s.now().UTC()
		if found.Status != TaskLeased {
			return ErrConflict
		}
		if found.LeaseVersion != version {
			return ErrStaleClaim
		}
		found.Status = TaskPending
		found.LeaseOwnerSessionID = ""
		found.LeaseOwnerInstance = nil
		found.LeaseExpiresAt = nil
		found.LeaseTokenHash = ""
		found.LeaseVersion++
		found.UpdatedAt = now
		t = found
		return w.PutTask(found)
	})
	if e != nil {
		return Task{}, e
	}
	return t, nil
}
