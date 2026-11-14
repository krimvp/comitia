package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"time"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/schema"
)

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
