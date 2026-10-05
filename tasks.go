package comitia

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskLeased    TaskStatus = "leased"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

type Task struct {
	ID, ContextID, Title, Body string
	OutcomeReason              string `json:"outcome_reason,omitempty"`
	Status                     TaskStatus
	LeaseOwnerSessionID        string
	LeaseVersion               int64
	LeaseExpiresAt             *time.Time
	LeaseTokenHash             string `json:"-"`
	CreatedAt, UpdatedAt       time.Time
	LeaseOwnerInstance         *AgentInstance `json:"lease_owner_instance,omitempty"`
	LeaseExpired               bool           `json:"lease_expired,omitempty"`
}
type TaskClaim struct {
	Task  Task
	Token string
}
type TaskInput struct {
	Title, Body string
	OperationID string `json:"operation_id,omitempty"`
}

// TaskReceipt records a non-secret mutation outcome. Tokens are never persisted.
type TaskReceipt struct {
	Fingerprint string
	Result      Task
}

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

// TaskMutationAPI is optional; ordinary task calls retain their original interface.
type TaskMutationAPI interface {
	CreateTask(context.Context, string, string, TaskInput) (Task, error)
	ClaimTaskWithOperation(context.Context, string, string, time.Duration, string, ...AgentInstance) (TaskClaim, error)
	RenewClaimWithOperation(context.Context, string, string, string, int64, time.Duration, string) (TaskClaim, error)
}

type TaskReceiptReadStore interface {
	TaskReceipt(string, string) (TaskReceipt, error)
}
type TaskReceiptWriteStore interface {
	PutTaskReceipt(string, string, TaskReceipt) error
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
	eventContext := ""
	if e := validOperationID(in.OperationID); e != nil {
		return t, e
	}
	if in.Title == "" {
		return t, Invalidf("title required")
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		a, _, e := s.require(w, raw, cid, PermissionWrite)
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
		t = Task{ID: id(), ContextID: cid, Title: in.Title, Body: in.Body, Status: TaskPending, CreatedAt: now, UpdatedAt: now}
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
		return Task{}, e
	}
	s.notifyTaskEvent(eventContext)
	return t, e
}
func (s *Service) GetTask(ctx context.Context, raw, id string) (Task, error) {
	var t Task
	e := s.store.View(ctx, func(r ReadStore) error {
		found, e := s.privateTask(r, raw, id)
		if e != nil {
			return e
		}
		if _, _, e = s.require(r, raw, found.ContextID, PermissionRead); e != nil {
			return e
		}
		t, e = s.taskLeaseView(r, found, s.now().UTC())
		return e
	})
	if e != nil {
		return Task{}, e
	}
	return t, nil
}
func (s *Service) ListTasks(ctx context.Context, raw, cid string, status TaskStatus, limit int) ([]Task, error) {
	var out []Task
	if limit < 1 || limit > 1000 {
		return nil, Invalidf("limit must be between 1 and 1000")
	}
	if !validTaskStatusFilter(status) {
		return nil, Invalidf("status must be pending, leased, completed, failed or cancelled")
	}
	e := s.store.View(ctx, func(r ReadStore) error {
		if _, _, e := s.require(r, raw, cid, PermissionRead); e != nil {
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
	return out, e
}

// The optional instance records which process of a shared session holds the
// claim. It is self-reported routing metadata and confers no permissions.
func (s *Service) ClaimTask(ctx context.Context, raw, taskID string, lease time.Duration, instance ...AgentInstance) (TaskClaim, error) {
	return s.ClaimTaskWithOperation(ctx, raw, taskID, lease, "", instance...)
}

// ClaimTaskWithOperation retries an identical operation without acquiring another lease.
// A retry rotates the token while the recorded lease is still current.
func (s *Service) ClaimTaskWithOperation(ctx context.Context, raw, taskID string, lease time.Duration, operationID string, instance ...AgentInstance) (TaskClaim, error) {
	var c TaskClaim
	eventContext := ""
	if e := validOperationID(operationID); e != nil {
		return c, e
	}
	if !validTTL(lease) || len(instance) > 1 {
		return c, Invalidf("lease must be positive and agent_instance accepts at most one instance")
	}
	var owner *AgentInstance
	if len(instance) == 1 {
		var e error
		if owner, e = normalizeAgentInstance(&instance[0]); e != nil {
			return c, e
		}
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		t, e := s.privateTask(w, raw, taskID)
		if e != nil {
			return e
		}
		a, _, e := s.require(w, raw, t.ContextID, PermissionClaim)
		if e != nil {
			return e
		}
		fingerprint := taskFingerprint("claim", taskID, lease, owner)
		receipt, found, e := taskReceipt(w, a.session.ID, operationID, fingerprint)
		if e != nil {
			return e
		}
		if found {
			c, e = s.recoverTaskClaim(w, t, receipt, a.session.ID)
			return e
		}
		now := s.now().UTC()
		if terminalTaskStatus(t.Status) {
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
		previous := t.Status
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
		if e := w.PutTask(t); e != nil {
			return e
		}
		if e := putTaskEvent(w, a.session.ID, t, "claimed", previous); e != nil {
			return e
		}
		eventContext = t.ContextID
		return putTaskReceipt(w, a.session.ID, operationID, fingerprint, c.Task)
	})
	if e != nil {
		return TaskClaim{}, e
	}
	s.notifyTaskEvent(eventContext)
	return c, e
}
func (s *Service) RenewClaim(ctx context.Context, raw, taskID, claimToken string, version int64, lease time.Duration) (TaskClaim, error) {
	return s.RenewClaimWithOperation(ctx, raw, taskID, claimToken, version, lease, "")
}
func (s *Service) RenewClaimWithOperation(ctx context.Context, raw, taskID, claimToken string, version int64, lease time.Duration, operationID string) (TaskClaim, error) {
	return s.changeLease(ctx, raw, taskID, claimToken, version, lease, false, operationID)
}
func (s *Service) CompleteTask(ctx context.Context, raw, taskID, claimToken string, version int64) (Task, error) {
	c, e := s.changeLease(ctx, raw, taskID, claimToken, version, 0, true, "")
	return c.Task, e
}
func (s *Service) changeLease(ctx context.Context, raw, taskID, claimToken string, version int64, lease time.Duration, complete bool, operationID string) (TaskClaim, error) {
	var c TaskClaim
	eventContext := ""
	if e := validOperationID(operationID); e != nil {
		return c, e
	}
	if !complete && !validTTL(lease) {
		return c, Invalidf("lease must be positive")
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		t, e := s.privateTask(w, raw, taskID)
		if e != nil {
			return e
		}
		a, _, e := s.require(w, raw, t.ContextID, PermissionClaim)
		if e != nil {
			return e
		}
		fingerprint := taskFingerprint("renew", taskID, tokenHash(claimToken), version, lease)
		receipt, found, e := taskReceipt(w, a.session.ID, operationID, fingerprint)
		if e != nil {
			return e
		}
		if found {
			c, e = s.recoverTaskClaim(w, t, receipt, a.session.ID)
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
		if e := w.PutTask(t); e != nil {
			return e
		}
		kind := "renewed"
		if complete {
			kind = "completed"
		}
		if e := putTaskEvent(w, a.session.ID, t, kind, TaskLeased); e != nil {
			return e
		}
		eventContext = t.ContextID
		return putTaskReceipt(w, a.session.ID, operationID, fingerprint, c.Task)
	})
	if e != nil {
		return TaskClaim{}, e
	}
	s.notifyTaskEvent(eventContext)
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
	eventContext := ""
	e := s.store.Update(ctx, func(w WriteStore) error {
		found, e := s.privateTask(w, raw, taskID)
		if e != nil {
			return e
		}
		a, _, e := s.require(w, raw, found.ContextID, PermissionClaim)
		holder := e == nil && found.LeaseOwnerSessionID == a.session.ID && claimToken != "" && subtle.ConstantTimeCompare([]byte(found.LeaseTokenHash), []byte(tokenHash(claimToken))) == 1
		if !holder {
			if a, _, e = s.require(w, raw, found.ContextID, PermissionAdmin); e != nil {
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
		if e := w.PutTask(found); e != nil {
			return e
		}
		if e := putTaskEvent(w, a.session.ID, found, "released", TaskLeased); e != nil {
			return e
		}
		eventContext = found.ContextID
		return nil
	})
	if e != nil {
		return Task{}, e
	}
	s.notifyTaskEvent(eventContext)
	return t, nil
}

// Authenticate before consulting private IDs. Missing and unreadable tasks have
// the same public result, even for administrators without context access.
func (s *Service) privateTask(r ReadStore, raw, taskID string) (Task, error) {
	if _, err := s.auth(r, raw); err != nil {
		return Task{}, err
	}
	t, err := r.Task(taskID)
	if errors.Is(err, ErrNotFound) {
		return Task{}, ErrForbidden
	}
	return t, err
}

// Recovery changes only the token hash; it cannot extend or resurrect a lease.
func (s *Service) recoverTaskClaim(w TaskWriteStore, current, result Task, sid string) (TaskClaim, error) {
	now := s.now().UTC()
	if current.ID != result.ID || current.ContextID != result.ContextID || result.Status != TaskLeased || result.LeaseOwnerSessionID != sid || current.Status != TaskLeased || current.LeaseOwnerSessionID != sid || current.LeaseVersion != result.LeaseVersion || current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(now) {
		return TaskClaim{}, fmt.Errorf("%w: recorded lease is no longer current", ErrConflict)
	}
	c := TaskClaim{Task: result, Token: token()}
	c.Task.LeaseTokenHash = ""
	current.LeaseTokenHash = tokenHash(c.Token)
	if e := w.PutTask(current); e != nil {
		return TaskClaim{}, e
	}
	return c, nil
}

func validOperationID(operationID string) error {
	if len(operationID) > 256 {
		return Invalidf("operation_id must be at most 256 bytes")
	}
	return nil
}

func taskFingerprint(parts ...any) string {
	raw, _ := json.Marshal(parts)
	return tokenHash(string(raw))
}

func taskReceipt(r TaskReadStore, sid, operationID, fingerprint string) (Task, bool, error) {
	if operationID == "" {
		return Task{}, false, nil
	}
	store, ok := r.(TaskReceiptReadStore)
	if !ok {
		return Task{}, false, Invalidf("task mutation receipts unsupported by store")
	}
	receipt, e := store.TaskReceipt(sid, operationID)
	if errors.Is(e, ErrNotFound) {
		return Task{}, false, nil
	}
	if e != nil {
		return Task{}, false, e
	}
	if receipt.Fingerprint != fingerprint {
		return Task{}, false, ErrConflict
	}
	return receipt.Result, true, nil
}

func putTaskReceipt(w TaskWriteStore, sid, operationID, fingerprint string, result Task) error {
	if operationID == "" {
		return nil
	}
	store, ok := w.(TaskReceiptWriteStore)
	if !ok {
		return Invalidf("task mutation receipts unsupported by store")
	}
	return store.PutTaskReceipt(sid, operationID, TaskReceipt{Fingerprint: fingerprint, Result: result})
}

// CreateTaskOperation rejects recoverable calls through APIs without that capability.
func CreateTaskOperation(ctx context.Context, api TaskAPI, raw, cid string, in TaskInput) (Task, error) {
	if in.OperationID != "" {
		if _, ok := api.(TaskMutationAPI); !ok {
			return Task{}, Invalidf("task mutation receipts unsupported by API")
		}
	}
	return api.CreateTask(ctx, raw, cid, in)
}

// ClaimTaskOperation selects recovery only when an operation ID was supplied.
func ClaimTaskOperation(ctx context.Context, api TaskAPI, raw, taskID string, lease time.Duration, operationID string, instance ...AgentInstance) (TaskClaim, error) {
	if operationID == "" {
		return api.ClaimTask(ctx, raw, taskID, lease, instance...)
	}
	recovery, ok := api.(TaskMutationAPI)
	if !ok {
		return TaskClaim{}, Invalidf("task mutation receipts unsupported by API")
	}
	return recovery.ClaimTaskWithOperation(ctx, raw, taskID, lease, operationID, instance...)
}
func RenewClaimOperation(ctx context.Context, api TaskAPI, raw, taskID, claimToken string, version int64, lease time.Duration, operationID string) (TaskClaim, error) {
	if operationID == "" {
		return api.RenewClaim(ctx, raw, taskID, claimToken, version, lease)
	}
	recovery, ok := api.(TaskMutationAPI)
	if !ok {
		return TaskClaim{}, Invalidf("task mutation receipts unsupported by API")
	}
	return recovery.RenewClaimWithOperation(ctx, raw, taskID, claimToken, version, lease, operationID)
}
