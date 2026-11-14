package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/krimvp/comitia/internal/schema"

	"github.com/krimvp/comitia/internal/model"
)

// RecoverClaim selects holder recovery without changing the original task API.
func RecoverClaim(ctx context.Context, api model.TaskAPI, raw, taskID string, version int64, instance ...model.AgentInstance) (model.TaskClaim, error) {
	recovery, ok := api.(model.TaskClaimRecoveryAPI)
	if !ok {
		return model.TaskClaim{}, model.Invalidf("claim recovery unsupported by this API")
	}
	return recovery.RecoverClaim(ctx, raw, taskID, version, instance...)
}

// RecoverClaim rotates a live holder's token, preserving version and expiry.
// Instance IDs fence processes sharing a session but are still self-reported.
func (s *Service) RecoverClaim(ctx context.Context, raw, taskID string, version int64, instance ...model.AgentInstance) (model.TaskClaim, error) {
	var c model.TaskClaim
	if taskID == "" || version < 1 || len(instance) > 1 {
		return c, model.Invalidf("task_id and positive lease_version required; agent_instance accepts at most one instance")
	}
	var owner *model.AgentInstance
	if len(instance) == 1 {
		var e error
		if owner, e = model.NormalizeAgentInstance(&instance[0]); e != nil {
			return c, e
		}
	}
	e := s.store.Update(ctx, func(w model.WriteStore) error {
		t, e := s.privateTask(w, raw, taskID)
		if e != nil {
			return e
		}
		if _, _, e := s.require(w, raw, t.ContextID, model.PermissionRead); e != nil {
			return e
		}
		a, _, e := s.require(w, raw, t.ContextID, model.PermissionClaim)
		if e != nil {
			return e
		}
		if t.Status != model.TaskLeased || t.LeaseOwnerSessionID != a.session.ID || t.LeaseVersion != version || t.LeaseExpiresAt == nil || !t.LeaseExpiresAt.After(s.now().UTC()) {
			return model.ErrStaleClaim
		}
		if t.LeaseOwnerInstance != nil && (owner == nil || owner.ID != t.LeaseOwnerInstance.ID) {
			return model.ErrStaleClaim
		}
		c = model.TaskClaim{Task: t, Token: token()}
		c.Task.LeaseTokenHash = ""
		t.LeaseTokenHash = tokenHash(c.Token)
		return w.PutTask(t)
	})
	if e != nil {
		return model.TaskClaim{}, e
	}
	return c, nil
}

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
	if limit < 1 || limit > 1000 {
		return nil, model.Invalidf("limit must be between 1 and 1000")
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

// The optional instance records which process of a shared session holds the
// claim. It is self-reported routing metadata and confers no permissions.
func (s *Service) ClaimTask(ctx context.Context, raw, taskID string, lease time.Duration, instance ...model.AgentInstance) (model.TaskClaim, error) {
	return s.ClaimTaskWithOperation(ctx, raw, taskID, lease, "", instance...)
}

// ClaimTaskWithOperation retries an identical operation without acquiring another lease.
// A retry rotates the token while the recorded lease is still current.
func (s *Service) ClaimTaskWithOperation(ctx context.Context, raw, taskID string, lease time.Duration, operationID string, instance ...model.AgentInstance) (model.TaskClaim, error) {
	var c model.TaskClaim
	eventContext := ""
	if e := validOperationID(operationID); e != nil {
		return c, e
	}
	if len(instance) > 1 {
		return c, model.Invalidf("agent_instance accepts at most one instance")
	}
	if e := validLease(lease); e != nil {
		return c, e
	}
	var owner *model.AgentInstance
	if len(instance) == 1 {
		var e error
		if owner, e = model.NormalizeAgentInstance(&instance[0]); e != nil {
			return c, e
		}
	}
	e := s.store.Update(ctx, func(w model.WriteStore) error {
		t, e := s.privateTask(w, raw, taskID)
		if e != nil {
			return e
		}
		a, _, e := s.require(w, raw, t.ContextID, model.PermissionClaim)
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
			if e == nil {
				s.touchInstanceIn(w, a.session.ID, t.ContextID, t.LeaseOwnerInstance)
				c.Task, e = s.holderTaskView(w, a, c.Task)
			}
			return e
		}
		now := s.now().UTC()
		if schema.TerminalTaskStatus(t.Status) {
			return model.ErrConflict
		}
		if t.LeaseExpiresAt != nil && t.LeaseExpiresAt.After(now) {
			live, e := s.holderCanClaim(w, t, now)
			if e != nil {
				return e
			}
			if live {
				return model.ErrConflict
			}
		}
		previous := t.Status
		t.Status = model.TaskLeased
		t.LeaseOwnerSessionID = a.session.ID
		t.LeaseOwnerInstance = owner
		t.LeaseVersion++
		exp := leaseExpiry(now, lease, a.session)
		t.LeaseExpiresAt = &exp
		t.UpdatedAt = now
		c = model.TaskClaim{Task: t, Token: token()}
		t.LeaseTokenHash = tokenHash(c.Token)
		c.Task = t
		c.Task.LeaseTokenHash = ""
		if e := w.PutTask(t); e != nil {
			return e
		}
		if e := putTaskEvent(w, a.session.ID, t, "claimed", previous); e != nil {
			return e
		}
		s.touchInstanceIn(w, a.session.ID, t.ContextID, t.LeaseOwnerInstance)
		eventContext = t.ContextID
		if e := putTaskReceipt(w, a.session.ID, operationID, fingerprint, c.Task); e != nil {
			return e
		}
		c.Task, e = s.holderTaskView(w, a, c.Task)
		return e
	})
	if e != nil {
		return model.TaskClaim{}, e
	}
	s.notifyTaskEvent(eventContext)
	return c, e
}

func (s *Service) RenewClaim(ctx context.Context, raw, taskID, claimToken string, version int64, lease time.Duration) (model.TaskClaim, error) {
	return s.RenewClaimWithOperation(ctx, raw, taskID, claimToken, version, lease, "")
}

func (s *Service) RenewClaimWithOperation(ctx context.Context, raw, taskID, claimToken string, version int64, lease time.Duration, operationID string) (model.TaskClaim, error) {
	return s.changeLease(ctx, raw, taskID, claimToken, version, lease, false, operationID)
}

func (s *Service) CompleteTask(ctx context.Context, raw, taskID, claimToken string, version int64) (model.Task, error) {
	c, e := s.changeLease(ctx, raw, taskID, claimToken, version, 0, true, "")
	return c.Task, e
}

func (s *Service) changeLease(ctx context.Context, raw, taskID, claimToken string, version int64, lease time.Duration, complete bool, operationID string) (model.TaskClaim, error) {
	var c model.TaskClaim
	eventContext := ""
	if e := validOperationID(operationID); e != nil {
		return c, e
	}
	if !complete {
		if e := validLease(lease); e != nil {
			return c, e
		}
	}
	e := s.store.Update(ctx, func(w model.WriteStore) error {
		t, e := s.privateTask(w, raw, taskID)
		if e != nil {
			return e
		}
		a, _, e := s.require(w, raw, t.ContextID, model.PermissionClaim)
		if e != nil {
			return e
		}
		instance := t.LeaseOwnerInstance
		if instance != nil {
			instance = &model.AgentInstance{ID: instance.ID}
		}
		fingerprint := taskFingerprint("renew", taskID, tokenHash(claimToken), version, lease)
		receipt, found, e := taskReceipt(w, a.session.ID, operationID, fingerprint)
		if e != nil {
			return e
		}
		if found {
			c, e = s.recoverTaskClaim(w, t, receipt, a.session.ID)
			if e == nil {
				s.touchInstanceIn(w, a.session.ID, t.ContextID, instance)
				c.Task, e = s.holderTaskView(w, a, c.Task)
			}
			return e
		}
		now := s.now().UTC()
		if t.Status != model.TaskLeased || t.LeaseOwnerSessionID != a.session.ID || t.LeaseVersion != version || t.LeaseExpiresAt == nil || !t.LeaseExpiresAt.After(now) || subtle.ConstantTimeCompare([]byte(t.LeaseTokenHash), []byte(tokenHash(claimToken))) != 1 {
			return model.ErrStaleClaim
		}
		if complete {
			t.Status = model.TaskCompleted
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
		if e := putTaskEvent(w, a.session.ID, t, kind, model.TaskLeased); e != nil {
			return e
		}
		s.touchInstanceIn(w, a.session.ID, t.ContextID, instance)
		eventContext = t.ContextID
		if e := putTaskReceipt(w, a.session.ID, operationID, fingerprint, c.Task); e != nil {
			return e
		}
		c.Task, e = s.holderTaskView(w, a, c.Task)
		return e
	})
	if e != nil {
		return model.TaskClaim{}, e
	}
	s.notifyTaskEvent(eventContext)
	return c, e
}

// A lease whose holder session was revoked, expired or lost claim on the
// context can never be renewed or completed, so it counts as expired.
func (s *Service) holderCanClaim(r model.ReadStore, t model.Task, now time.Time) (bool, error) {
	holder, e := r.Session(t.LeaseOwnerSessionID)
	if errors.Is(e, model.ErrNotFound) {
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
		if g.Permissions&model.PermissionClaim != 0 {
			return true, nil
		}
	}
	return false, nil
}

// Leases never outlive the session that holds them, which also bounds OpenLease.
func leaseExpiry(now time.Time, lease time.Duration, holder model.Session) time.Time {
	if lease >= holder.ExpiresAt.Sub(now) {
		return holder.ExpiresAt
	}
	return now.Add(lease)
}

// ReleaseTask returns a leased task to pending. The holder releases with its
// claim token; a context admin may release without it, e.g. to free a task whose
// worker went silent, but must name the current lease version. Releasing an
// expired lease is allowed and just tidies the task.
func (s *Service) ReleaseTask(ctx context.Context, raw, taskID, claimToken string, version int64) (model.Task, error) {
	var t model.Task
	eventContext := ""
	e := s.store.Update(ctx, func(w model.WriteStore) error {
		found, e := s.privateTask(w, raw, taskID)
		if e != nil {
			return e
		}
		a, _, e := s.require(w, raw, found.ContextID, model.PermissionClaim)
		holder := e == nil && found.LeaseOwnerSessionID == a.session.ID && claimToken != "" && subtle.ConstantTimeCompare([]byte(found.LeaseTokenHash), []byte(tokenHash(claimToken))) == 1
		if !holder {
			if a, _, e = s.require(w, raw, found.ContextID, model.PermissionAdmin); e != nil {
				return e
			}
		}
		now := s.now().UTC()
		if found.Status != model.TaskLeased {
			return model.ErrConflict
		}
		if found.LeaseVersion != version {
			return model.ErrStaleClaim
		}
		instance := holderInstance(found, a.session.ID)
		found.Status = model.TaskPending
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
		if e := putTaskEventAs(w, a.session.ID, instance, found, "released", model.TaskLeased); e != nil {
			return e
		}
		eventContext = found.ContextID
		t, e = s.holderTaskView(w, a, t)
		return e
	})
	if e != nil {
		return model.Task{}, e
	}
	s.notifyTaskEvent(eventContext)
	return t, nil
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

// Recovery changes only the token hash; it cannot extend or resurrect a lease.
func (s *Service) recoverTaskClaim(w model.TaskWriteStore, current, result model.Task, sid string) (model.TaskClaim, error) {
	now := s.now().UTC()
	if current.ID != result.ID || current.ContextID != result.ContextID || result.Status != model.TaskLeased || result.LeaseOwnerSessionID != sid || current.Status != model.TaskLeased || current.LeaseOwnerSessionID != sid || current.LeaseVersion != result.LeaseVersion || current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(now) {
		return model.TaskClaim{}, fmt.Errorf("%w: recorded lease is no longer current", model.ErrConflict)
	}
	c := model.TaskClaim{Task: result, Token: token()}
	c.Task.LeaseTokenHash = ""
	current.LeaseTokenHash = tokenHash(c.Token)
	if e := w.PutTask(current); e != nil {
		return model.TaskClaim{}, e
	}
	return c, nil
}

func validOperationID(operationID string) error {
	if len(operationID) > 256 {
		return model.Invalidf("operation_id must be at most 256 bytes")
	}
	return nil
}

func taskFingerprint(parts ...any) string {
	raw, _ := json.Marshal(parts)
	return tokenHash(string(raw))
}

func taskReceipt(r model.TaskReadStore, sid, operationID, fingerprint string) (model.Task, bool, error) {
	if operationID == "" {
		return model.Task{}, false, nil
	}
	store, ok := r.(model.TaskReceiptReadStore)
	if !ok {
		return model.Task{}, false, model.Invalidf("task mutation receipts unsupported by store")
	}
	receipt, e := store.TaskReceipt(sid, operationID)
	if errors.Is(e, model.ErrNotFound) {
		return model.Task{}, false, nil
	}
	if e != nil {
		return model.Task{}, false, e
	}
	if receipt.Fingerprint != fingerprint {
		return model.Task{}, false, model.ErrConflict
	}
	// Titles are required, so an empty one marks a compact receipt. Receipts
	// written by older binaries carry the full task and are returned as stored.
	if receipt.Result.Title == "" {
		t, e := r.Task(receipt.Result.ID)
		if e != nil {
			return model.Task{}, false, e
		}
		receipt.Result.Title, receipt.Result.Body = t.Title, t.Body
	}
	return receipt.Result, true, nil
}

func putTaskReceipt(w model.TaskWriteStore, sid, operationID, fingerprint string, result model.Task) error {
	if operationID == "" {
		return nil
	}
	store, ok := w.(model.TaskReceiptWriteStore)
	if !ok {
		return model.Invalidf("task mutation receipts unsupported by store")
	}
	// Title and body never change after create; retries re-read them from the
	// task so each receipt stays small however large the task is.
	result.Title, result.Body = "", ""
	return store.PutTaskReceipt(sid, operationID, model.TaskReceipt{Fingerprint: fingerprint, Result: result})
}

// CreateTaskOperation rejects recoverable calls through APIs without that capability.
func CreateTaskOperation(ctx context.Context, api model.TaskAPI, raw, cid string, in model.TaskInput) (model.Task, error) {
	if in.OperationID != "" {
		if _, ok := api.(model.TaskMutationAPI); !ok {
			return model.Task{}, model.Invalidf("task mutation receipts unsupported by API")
		}
	}
	return api.CreateTask(ctx, raw, cid, in)
}

// ClaimTaskOperation selects recovery only when an operation ID was supplied.
func ClaimTaskOperation(ctx context.Context, api model.TaskAPI, raw, taskID string, lease time.Duration, operationID string, instance ...model.AgentInstance) (model.TaskClaim, error) {
	if operationID == "" {
		return api.ClaimTask(ctx, raw, taskID, lease, instance...)
	}
	recovery, ok := api.(model.TaskMutationAPI)
	if !ok {
		return model.TaskClaim{}, model.Invalidf("task mutation receipts unsupported by API")
	}
	return recovery.ClaimTaskWithOperation(ctx, raw, taskID, lease, operationID, instance...)
}

func RenewClaimOperation(ctx context.Context, api model.TaskAPI, raw, taskID, claimToken string, version int64, lease time.Duration, operationID string) (model.TaskClaim, error) {
	if operationID == "" {
		return api.RenewClaim(ctx, raw, taskID, claimToken, version, lease)
	}
	recovery, ok := api.(model.TaskMutationAPI)
	if !ok {
		return model.TaskClaim{}, model.Invalidf("task mutation receipts unsupported by API")
	}
	return recovery.RenewClaimWithOperation(ctx, raw, taskID, claimToken, version, lease, operationID)
}
