// Task tools: create, claim, recover, renew, complete, release, get and list.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/krimvp/comitia"
)

func taskTools() []map[string]any {
	return []map[string]any{
		tool("fail_task", "Fail a task permanently with a live claim and reason; release_task remains retryable", taskOutcomeSchema(false)),
		tool("cancel_task", "Cancel a task with a live claim or context-admin authority and current version; admins may cancel pending work at version 0", taskOutcomeSchema(true)),
		tool("create_task", "Create a task in a context", schema(map[string]any{"context_id": str("Context ID"), "title": limited("Task title", comitia.MaxTaskTitleBytes), "body": limited("Task details", comitia.MaxTaskBodyBytes), "operation_id": taskOperationID()}, "context_id", "title")),
		tool("claim_task", "Claim a pending task; lease_seconds "+leaseSecondsDescription+", or use open_ended. Returns Task + Token; keep Token and Task.LeaseVersion for renew/complete. Omitted agent_instance defaults to COMITIA_AGENT_INSTANCE / COMITIA_AGENT_INSTANCE_LABEL; explicit objects replace both defaults and an empty ID is invalid", schema(map[string]any{"task_id": str("Task ID"), "operation_id": taskOperationID(), "lease_seconds": integer("Lease duration in seconds"), "open_ended": openEnded(), "agent_instance": schema(map[string]any{"id": str("Self-reported instance ID holding the claim: 1-64 ASCII letters, digits, dot, underscore or hyphen; never identity proof"), "label": str("Optional self-reported display label (at most 128 bytes)")}, "id")}, "task_id")),
		tool("recover_claim", "Recover a lost claim token for your own live lease at its current version; matching agent_instance ID required for instance claims. Rotates Token without extending expiry or changing LeaseVersion; earlier tokens become stale. Omitted agent_instance uses configured defaults.", schema(map[string]any{"task_id": str("Task ID"), "lease_version": integer("Current positive lease version from get_task"), "agent_instance": schema(map[string]any{"id": str("Matching holder instance ID; self-reported, never identity proof"), "label": str("Optional display label")}, "id")}, "task_id", "lease_version")),
		tool("renew_task", "Renew a task claim using claim_token and lease_version; lease_seconds "+leaseSecondsDescription+", or use open_ended. Returns Task + Token; replace both Token and Task.LeaseVersion after renewal.", schema(map[string]any{"task_id": str("Task ID"), "claim_token": str("Opaque claim token"), "lease_version": integer("Current lease version"), "operation_id": taskOperationID(), "lease_seconds": integer("Lease duration in seconds"), "open_ended": openEnded()}, "task_id", "claim_token", "lease_version")),
		tool("complete_task", "Complete a claimed task using the latest Token as claim_token and Task.LeaseVersion as lease_version from claim/renew.", schema(map[string]any{"task_id": str("Task ID"), "claim_token": str("Opaque claim token"), "lease_version": integer("Current lease version")}, "task_id", "claim_token", "lease_version")),
		tool("release_task", "Return a leased task to pending; the holder passes its claim token, a context admin may omit it", schema(map[string]any{"task_id": str("Task ID"), "claim_token": str("Opaque claim token; optional for a context admin"), "lease_version": integer("Current lease version")}, "task_id", "lease_version")),
		tool("get_task", "Get a task by ID", schema(map[string]any{"task_id": str("Task ID")}, "task_id")),
		tool("list_tasks", "List tasks in a context; lease_expired marks recoverable leases; claimable selects pending or expired, owner filters select live leases", schema(map[string]any{"claimable": map[string]any{"type": "boolean", "description": "Pending tasks and expired leases"}, "owner_session": str("Session ID holding live leases; excludes mine"), "owner_instance": str("Instance ID holding live leases"), "mine": map[string]any{"type": "boolean", "description": "Caller live leases, scoped to configured instance when available"}, "context_id": str("Context ID"), "status": map[string]any{"type": "string", "enum": []string{"", "pending", "leased", "completed", "failed", "cancelled"}}, "limit": integer("Maximum tasks"), "page": map[string]any{"type": "boolean", "description": "Return tasks, next_after and has_more"}, "after_id": str("Exclusive opaque task cursor from next_after; implies page")}, "context_id")),
	}
}

func (s *Server) callTasks(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "fail_task", "cancel_task":
		return s.callTaskOutcome(ctx, name, raw)
	case "create_task":
		return s.callCreateTask(ctx, raw)
	case "claim_task":
		return s.callClaimTask(ctx, raw)
	case "recover_claim":
		return s.callRecoverClaim(ctx, raw)
	case "renew_task":
		return s.callRenewTask(ctx, raw)
	case "complete_task":
		return s.callCompleteTask(ctx, raw)
	case "release_task":
		return s.callReleaseTask(ctx, raw)
	case "get_task":
		return s.callGetTask(ctx, raw)
	case "list_tasks":
		return s.callListTasks(ctx, raw)
	default:
		return nil, errUnknownTool
	}
}

func (s *Server) callCreateTask(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		OperationID string `json:"operation_id"`
		ContextID   string `json:"context_id"`
		Title       string `json:"title"`
		Body        string `json:"body"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if err := required("context_id", a.ContextID, "title", a.Title); err != nil {
		return nil, err
	}
	return comitia.CreateTaskOperation(ctx, s.API, s.Credential, a.ContextID, comitia.TaskInput{Title: a.Title, Body: a.Body, OperationID: a.OperationID})
}

func (s *Server) callClaimTask(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		OperationID   string                 `json:"operation_id"`
		TaskID        string                 `json:"task_id"`
		LeaseSeconds  int64                  `json:"lease_seconds"`
		OpenEnded     bool                   `json:"open_ended"`
		AgentInstance *comitia.AgentInstance `json:"agent_instance"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if err := required("task_id", a.TaskID); err != nil {
		return nil, err
	}
	d, e := lease(a.LeaseSeconds, a.OpenEnded)
	if e != nil {
		return nil, e
	}
	var instance []comitia.AgentInstance
	if owner := s.InstanceDefaults.Message(a.AgentInstance); owner != nil {
		instance = append(instance, *owner)
	}
	return comitia.ClaimTaskOperation(ctx, s.API, s.Credential, a.TaskID, d, a.OperationID, instance...)
}

func (s *Server) callRecoverClaim(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		TaskID        string                 `json:"task_id"`
		LeaseVersion  *int64                 `json:"lease_version"`
		AgentInstance *comitia.AgentInstance `json:"agent_instance"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if err := required("task_id", a.TaskID); err != nil {
		return nil, err
	}
	if a.LeaseVersion == nil {
		return nil, comitia.Invalidf("lease_version required")
	}
	var instance []comitia.AgentInstance
	if owner := s.InstanceDefaults.Message(a.AgentInstance); owner != nil {
		instance = append(instance, *owner)
	}
	return comitia.RecoverClaim(ctx, s.API, s.Credential, a.TaskID, *a.LeaseVersion, instance...)
}

func (s *Server) callRenewTask(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		OperationID  string `json:"operation_id"`
		TaskID       string `json:"task_id"`
		ClaimToken   string `json:"claim_token"`
		LeaseVersion *int64 `json:"lease_version"`
		LeaseSeconds int64  `json:"lease_seconds"`
		OpenEnded    bool   `json:"open_ended"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if err := required("task_id", a.TaskID, "claim_token", a.ClaimToken); err != nil {
		return nil, err
	}
	if a.LeaseVersion == nil {
		return nil, comitia.Invalidf("lease_version required")
	}
	d, e := lease(a.LeaseSeconds, a.OpenEnded)
	if e != nil {
		return nil, e
	}
	return comitia.RenewClaimOperation(ctx, s.API, s.Credential, a.TaskID, a.ClaimToken, *a.LeaseVersion, d, a.OperationID)
}

func (s *Server) callCompleteTask(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		TaskID       string `json:"task_id"`
		ClaimToken   string `json:"claim_token"`
		LeaseVersion *int64 `json:"lease_version"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if err := required("task_id", a.TaskID, "claim_token", a.ClaimToken); err != nil {
		return nil, err
	}
	if a.LeaseVersion == nil {
		return nil, comitia.Invalidf("lease_version required")
	}
	return s.API.CompleteTask(ctx, s.Credential, a.TaskID, a.ClaimToken, *a.LeaseVersion)
}

func (s *Server) callReleaseTask(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		TaskID       string `json:"task_id"`
		ClaimToken   string `json:"claim_token"`
		LeaseVersion *int64 `json:"lease_version"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if err := required("task_id", a.TaskID); err != nil {
		return nil, err
	}
	if a.LeaseVersion == nil {
		return nil, comitia.Invalidf("lease_version required")
	}
	return s.API.ReleaseTask(ctx, s.Credential, a.TaskID, a.ClaimToken, *a.LeaseVersion)
}

func (s *Server) callGetTask(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		TaskID string `json:"task_id"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if err := required("task_id", a.TaskID); err != nil {
		return nil, err
	}
	return s.API.GetTask(ctx, s.Credential, a.TaskID)
}

func (s *Server) callListTasks(ctx context.Context, raw json.RawMessage) (any, error) {
	var a struct {
		Claimable     bool   `json:"claimable"`
		OwnerSession  string `json:"owner_session"`
		OwnerInstance string `json:"owner_instance"`
		Mine          bool   `json:"mine"`
		Page          bool   `json:"page"`
		AfterID       string `json:"after_id"`
		ContextID     string `json:"context_id"`
		Status        string `json:"status"`
		Limit         *int   `json:"limit"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if err := required("context_id", a.ContextID); err != nil {
		return nil, err
	}
	limit := comitia.DefaultListLimit
	if a.Limit != nil {
		limit = *a.Limit
	}
	if limit < 1 || limit > comitia.MaxListLimit {
		return nil, comitia.Invalidf("limit must be between 1 and %d", comitia.MaxListLimit)
	}
	status := comitia.TaskStatus(a.Status)
	if a.Status != "" && status != comitia.TaskPending && status != comitia.TaskLeased && status != comitia.TaskCompleted && status != comitia.TaskFailed && status != comitia.TaskCancelled {
		return nil, comitia.Invalidf("status must be pending, leased, completed, failed or cancelled")
	}
	f := (comitia.TaskFilters{Claimable: a.Claimable, OwnerSession: a.OwnerSession, OwnerInstance: a.OwnerInstance, Mine: a.Mine}).WithInstance(s.InstanceDefaults)
	if a.Page || a.AfterID != "" {
		return comitia.ListTaskPageFiltered(ctx, s.API, s.Credential, a.ContextID, status, f, a.AfterID, limit)
	}
	return comitia.ListTasksFiltered(ctx, s.API, s.Credential, a.ContextID, status, f, limit)
}

func openEnded() map[string]any {
	return map[string]any{"type": "boolean", "description": "Hold the lease until the task is completed or released (capped at this session's expiry); excludes lease_seconds"}
}

func lease(n int64, open bool) (time.Duration, error) {
	if open {
		if n != 0 {
			return 0, comitia.Invalidf("open_ended excludes lease_seconds")
		}
		return comitia.OpenLease, nil
	}
	return seconds(n)
}

// leaseSecondsDescription states the lease_seconds default and bounds.
var leaseSecondsDescription = fmt.Sprintf("defaults to %d (%d–%d)", leaseSeconds(comitia.DefaultLease), leaseSeconds(comitia.MinLease), leaseSeconds(comitia.MaxLease))

func leaseSeconds(d time.Duration) int64 { return int64(d / time.Second) }

func taskOperationID() map[string]any {
	return map[string]any{"type": "string", "maxLength": 256, "description": "Optional stable caller/session-scoped operation ID (at most 256 bytes). Persist before sending. Exact create retries recover the task; current-lease claim/renew retries return a fresh token with the same version, invalidating earlier tokens. Changed requests or stale leases conflict."}
}
