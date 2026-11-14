package comitia

import (
	"context"
	"time"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/service"
)

// Tasks and their leases, outcomes and receipts.

// TaskFilters narrow task reads without changing stored status or ownership.
type TaskFilters = model.TaskFilters

// TaskFilterAPI is optional; existing task interfaces retain their method sets.
type TaskFilterAPI = model.TaskFilterAPI

// TaskOutcomeAPI is optional so existing TaskAPI implementations remain valid.
type TaskOutcomeAPI = model.TaskOutcomeAPI

type TaskOutcomeInput = model.TaskOutcomeInput

type TaskStatus = model.TaskStatus

const (
	TaskPending   = model.TaskPending
	TaskLeased    = model.TaskLeased
	TaskCompleted = model.TaskCompleted
	TaskFailed    = model.TaskFailed
	TaskCancelled = model.TaskCancelled
)

type Task = model.Task

type TaskClaim = model.TaskClaim

type TaskInput = model.TaskInput

// TaskReceipt records a non-secret mutation outcome.
type TaskReceipt = model.TaskReceipt

const // OpenLease asks for a lease that lasts until the task is completed or
// released.
OpenLease = model.OpenLease

type TaskAPI = model.TaskAPI

// TaskMutationAPI is optional; ordinary task calls retain their original
// interface.
type TaskMutationAPI = model.TaskMutationAPI

// TaskClaimRecoveryAPI is optional; recovery never extends the current lease.
type TaskClaimRecoveryAPI = model.TaskClaimRecoveryAPI

// Discovery cursors are exclusive and never acknowledge messages.
type TaskPage = model.TaskPage

// Paging is optional: implementations of the original API/store contracts
// remain valid.
type TaskPageAPI = model.TaskPageAPI

func ListTaskPage(ctx context.Context, api TaskAPI, token, cid string, status TaskStatus, after string, limit int) (TaskPage, error) {
	return service.ListTaskPage(ctx, api, token, cid, status, after, limit)
}

// RecoverClaim selects holder recovery without changing the original task API.
func RecoverClaim(ctx context.Context, api TaskAPI, raw, taskID string, version int64, instance ...AgentInstance) (TaskClaim, error) {
	return service.RecoverClaim(ctx, api, raw, taskID, version, instance...)
}

// CreateTaskOperation rejects recoverable calls through APIs without that capability.
func CreateTaskOperation(ctx context.Context, api TaskAPI, raw, cid string, in TaskInput) (Task, error) {
	return service.CreateTaskOperation(ctx, api, raw, cid, in)
}

// ClaimTaskOperation selects recovery only when an operation ID was supplied.
func ClaimTaskOperation(ctx context.Context, api TaskAPI, raw, taskID string, lease time.Duration, operationID string, instance ...AgentInstance) (TaskClaim, error) {
	return service.ClaimTaskOperation(ctx, api, raw, taskID, lease, operationID, instance...)
}

func RenewClaimOperation(ctx context.Context, api TaskAPI, raw, taskID, claimToken string, version int64, lease time.Duration, operationID string) (TaskClaim, error) {
	return service.RenewClaimOperation(ctx, api, raw, taskID, claimToken, version, lease, operationID)
}

func ListTasksFiltered(ctx context.Context, api TaskAPI, raw, cid string, status TaskStatus, f TaskFilters, limit int) ([]Task, error) {
	return service.ListTasksFiltered(ctx, api, raw, cid, status, f, limit)
}

func ListTaskPageFiltered(ctx context.Context, api TaskAPI, raw, cid string, status TaskStatus, f TaskFilters, after string, limit int) (TaskPage, error) {
	return service.ListTaskPageFiltered(ctx, api, raw, cid, status, f, after, limit)
}

func FailTask(ctx context.Context, api TaskAPI, raw, tid string, in TaskOutcomeInput) (Task, error) {
	return service.FailTask(ctx, api, raw, tid, in)
}

func CancelTask(ctx context.Context, api TaskAPI, raw, tid string, in TaskOutcomeInput) (Task, error) {
	return service.CancelTask(ctx, api, raw, tid, in)
}
