package mcp

import (
	"context"
	"encoding/json"
	"time"

	"github.com/krimvp/comitia"
)

func taskTools() []map[string]any {
	return []map[string]any{
		tool("fail_task", "Fail a task permanently with a live claim and reason; release_task remains retryable", taskOutcomeSchema(false)),
		tool("cancel_task", "Cancel a task with a live claim or context-admin authority and current version; admins may cancel pending work at version 0", taskOutcomeSchema(true)),
		tool("create_task", "Create a task in a context", schema(map[string]any{"context_id": str("Context ID"), "title": str("Task title"), "body": str("Task details"), "operation_id": taskOperationID()}, "context_id", "title")),
		tool("claim_task", "Claim a pending task; lease_seconds defaults to 300 (1–86400), or use open_ended. Returns Task + Token; keep Token and Task.LeaseVersion for renew/complete. Omitted agent_instance defaults to COMITIA_AGENT_INSTANCE / COMITIA_AGENT_INSTANCE_LABEL; explicit objects replace both defaults and an empty ID is invalid", schema(map[string]any{"task_id": str("Task ID"), "operation_id": taskOperationID(), "lease_seconds": integer("Lease duration in seconds"), "open_ended": openEnded(), "agent_instance": schema(map[string]any{"id": str("Self-reported instance ID holding the claim: 1-64 ASCII letters, digits, dot, underscore or hyphen; never identity proof"), "label": str("Optional self-reported display label (at most 128 bytes)")}, "id")}, "task_id")),
		tool("renew_task", "Renew a task claim using claim_token and lease_version; lease_seconds defaults to 300 (1–86400), or use open_ended. Returns Task + Token; replace both Token and Task.LeaseVersion after renewal.", schema(map[string]any{"task_id": str("Task ID"), "claim_token": str("Opaque claim token"), "lease_version": integer("Current lease version"), "operation_id": taskOperationID(), "lease_seconds": integer("Lease duration in seconds"), "open_ended": openEnded()}, "task_id", "claim_token", "lease_version")),
		tool("complete_task", "Complete a claimed task using the latest Token as claim_token and Task.LeaseVersion as lease_version from claim/renew.", schema(map[string]any{"task_id": str("Task ID"), "claim_token": str("Opaque claim token"), "lease_version": integer("Current lease version")}, "task_id", "claim_token", "lease_version")),
		tool("release_task", "Return a leased task to pending; the holder passes its claim token, a context admin may omit it", schema(map[string]any{"task_id": str("Task ID"), "claim_token": str("Opaque claim token; optional for a context admin"), "lease_version": integer("Current lease version")}, "task_id", "lease_version")),
		tool("get_task", "Get a task by ID", schema(map[string]any{"task_id": str("Task ID")}, "task_id")),
		tool("list_tasks", "List tasks in a context", schema(map[string]any{"context_id": str("Context ID"), "status": map[string]any{"type": "string", "enum": []string{"pending", "leased", "completed", "failed", "cancelled"}}, "limit": integer("Maximum tasks"), "page": map[string]any{"type": "boolean", "description": "Return tasks, next_after and has_more"}, "after_id": str("Exclusive opaque task cursor from next_after; implies page")}, "context_id")),
	}
}

func (s *Server) callTasks(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "fail_task", "cancel_task":
		return s.callTaskOutcome(ctx, name, raw)
	case "create_task":
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
	case "claim_task":
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
	case "renew_task":
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
	case "complete_task":
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
	case "release_task":
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
	case "get_task":
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
	case "list_tasks":
		var a struct {
			Page      bool   `json:"page"`
			AfterID   string `json:"after_id"`
			ContextID string `json:"context_id"`
			Status    string `json:"status"`
			Limit     *int   `json:"limit"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("context_id", a.ContextID); err != nil {
			return nil, err
		}
		limit := 100
		if a.Limit != nil {
			limit = *a.Limit
		}
		if limit < 1 || limit > 1000 {
			return nil, comitia.Invalidf("limit must be between 1 and 1000")
		}
		status := comitia.TaskStatus(a.Status)
		if a.Status != "" && status != comitia.TaskPending && status != comitia.TaskLeased && status != comitia.TaskCompleted && status != comitia.TaskFailed && status != comitia.TaskCancelled {
			return nil, comitia.Invalidf("status must be pending, leased, completed, failed or cancelled")
		}
		if a.Page || a.AfterID != "" {
			return comitia.ListTaskPage(ctx, s.API, s.Credential, a.ContextID, status, a.AfterID, limit)
		}
		return s.API.ListTasks(ctx, s.Credential, a.ContextID, status, limit)
	default:
		return nil, errUnknownTool
	}
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
	return seconds(n, 300)
}

func taskOperationID() map[string]any {
	return map[string]any{"type": "string", "maxLength": 256, "description": "Optional stable caller/session-scoped operation ID (at most 256 bytes). Persist before sending. Exact create retries recover the task; current-lease claim/renew retries return a fresh token with the same version, invalidating earlier tokens. Changed requests or stale leases conflict."}
}
