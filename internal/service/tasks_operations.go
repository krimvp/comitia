package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
