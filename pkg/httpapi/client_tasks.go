package httpapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

// Servers before task mutation receipts reject operation_id while decoding, so
// the request was not applied. Clients must not retry it without idempotency.
var ErrTaskMutationReceiptsUnsupported = fmt.Errorf("%w: server does not support task mutation receipts (operation_id); upgrade the server before retrying", comitia.ErrInvalid)

var ErrClaimRecoveryUnsupported = fmt.Errorf("%w: claim recovery unsupported by this server; upgrade the server before retrying", comitia.ErrInvalid)

func (c *Client) CreateTask(ctx context.Context, token, id string, input comitia.TaskInput) (comitia.Task, error) {
	var out taskResult
	err := c.call(ctx, token, "tasks/create", createTaskRequest{id, ptr(taskInput(input))}, &out)
	if input.OperationID != "" && unknownField(err, "operation_id") {
		return comitia.Task{}, ErrTaskMutationReceiptsUnsupported
	}
	if err = requiredResult(err, out.Task); err != nil {
		return comitia.Task{}, err
	}
	return comitia.Task(*out.Task), nil
}
func (c *Client) GetTask(ctx context.Context, token, id string) (comitia.Task, error) {
	var out taskResult
	err := c.call(ctx, token, "tasks/get", taskRequest{id}, &out)
	if err = requiredResult(err, out.Task); err != nil {
		return comitia.Task{}, err
	}
	return comitia.Task(*out.Task), nil
}
func (c *Client) ListTasks(ctx context.Context, token, id string, status comitia.TaskStatus, limit int) ([]comitia.Task, error) {
	var out tasksResult
	err := c.call(ctx, token, "tasks/list", listTasksRequest{ContextID: id, Status: status, Limit: ptr(limit)}, &out)
	return mapSlice(out.Tasks, func(t taskDTO) comitia.Task { return comitia.Task(t) }), err
}

// ListTasksPage falls back to the bounded legacy list when the server predates
// task paging, preserving the same exhaustion guarantee as other legacy APIs.
func (c *Client) ListTasksPage(ctx context.Context, token, id string, status comitia.TaskStatus, after string, limit int) (comitia.TaskPage, error) {
	var out struct {
		TaskPage *taskPageDTO `json:"task_page,omitempty"`
	}
	err := c.call(ctx, token, "tasks/list", listTasksRequest{ContextID: id, Status: status, AfterID: after, Limit: ptr(limit), Page: true}, &out)
	if pagingUnsupported(err) || err == nil && out.TaskPage == nil {
		return comitia.ListTaskPage(ctx, legacyTaskLists{c}, token, id, status, after, limit)
	}
	if err != nil {
		return comitia.TaskPage{}, err
	}
	return out.TaskPage.core(), nil
}

// legacyTaskLists hides the Client's paging capability from the fallback.
type legacyTaskLists struct{ comitia.TaskAPI }

func (c *Client) ClaimTask(ctx context.Context, token, id string, lease time.Duration, instances ...comitia.AgentInstance) (comitia.TaskClaim, error) {
	return c.ClaimTaskWithOperation(ctx, token, id, lease, "", instances...)
}

// ClaimTaskWithOperation records a session-scoped receipt; see comitia.TaskMutationAPI.
func (c *Client) ClaimTaskWithOperation(ctx context.Context, token, id string, lease time.Duration, operationID string, instances ...comitia.AgentInstance) (comitia.TaskClaim, error) {
	q := claimRequest{TaskID: id, TTL: ptr(lease), OperationID: operationID}
	switch len(instances) {
	case 0:
		q.ClaimInstance = c.InstanceDefaults.Message(nil)
	case 1:
		q.ClaimInstance = &instances[0]
	default:
		return comitia.TaskClaim{}, comitia.Invalidf("claim_instance accepts at most one instance")
	}
	var out claimResult
	err := c.call(ctx, token, "tasks/claim", q, &out)
	if operationID != "" && unknownField(err, "operation_id") {
		return comitia.TaskClaim{}, ErrTaskMutationReceiptsUnsupported
	}
	if err = requiredResult(err, out.Claim); err != nil {
		return comitia.TaskClaim{}, err
	}
	return out.Claim.core(), nil
}
func (c *Client) RecoverClaim(ctx context.Context, token, id string, version int64, instances ...comitia.AgentInstance) (comitia.TaskClaim, error) {
	q := recoverClaimRequest{TaskID: id, LeaseVersion: ptr(version)}
	switch len(instances) {
	case 0:
		q.ClaimInstance = c.InstanceDefaults.Message(nil)
	case 1:
		q.ClaimInstance = &instances[0]
	default:
		return comitia.TaskClaim{}, comitia.Invalidf("claim_instance accepts at most one instance")
	}
	var out claimResult
	err := c.call(ctx, token, "tasks/recover", q, &out)
	if errors.Is(err, comitia.ErrNotFound) {
		return comitia.TaskClaim{}, ErrClaimRecoveryUnsupported
	}
	if err = requiredResult(err, out.Claim); err != nil {
		return comitia.TaskClaim{}, err
	}
	return out.Claim.core(), nil
}

var _ comitia.TaskClaimRecoveryAPI = (*Client)(nil)

func (c *Client) RenewClaim(ctx context.Context, token, id, key string, version int64, lease time.Duration) (comitia.TaskClaim, error) {
	return c.RenewClaimWithOperation(ctx, token, id, key, version, lease, "")
}
func (c *Client) RenewClaimWithOperation(ctx context.Context, token, id, key string, version int64, lease time.Duration, operationID string) (comitia.TaskClaim, error) {
	var out claimResult
	err := c.call(ctx, token, "tasks/renew", renewRequest{TaskID: id, ClaimToken: key, LeaseVersion: ptr(version), TTL: ptr(lease), OperationID: operationID}, &out)
	if operationID != "" && unknownField(err, "operation_id") {
		return comitia.TaskClaim{}, ErrTaskMutationReceiptsUnsupported
	}
	if err = requiredResult(err, out.Claim); err != nil {
		return comitia.TaskClaim{}, err
	}
	return out.Claim.core(), nil
}
func (c *Client) CompleteTask(ctx context.Context, token, id, key string, version int64) (comitia.Task, error) {
	var out taskResult
	err := c.call(ctx, token, "tasks/complete", finishTaskRequest{id, key, ptr(version)}, &out)
	if err = requiredResult(err, out.Task); err != nil {
		return comitia.Task{}, err
	}
	return comitia.Task(*out.Task), nil
}
func (c *Client) ReleaseTask(ctx context.Context, token, id, key string, version int64) (comitia.Task, error) {
	var out taskResult
	err := c.call(ctx, token, "tasks/release", finishTaskRequest{id, key, ptr(version)}, &out)
	if err = requiredResult(err, out.Task); err != nil {
		return comitia.Task{}, err
	}
	return comitia.Task(*out.Task), nil
}

var _ comitia.TaskMutationAPI = (*Client)(nil)

var ErrTaskFiltersUnsupported = fmt.Errorf("%w: server does not support task filters; upgrade the server before retrying", comitia.ErrInvalid)

func filteredTaskRequest(cid string, status comitia.TaskStatus, f comitia.TaskFilters, after string, limit int, page bool) listTasksRequest {
	return listTasksRequest{ContextID: cid, Status: status, Claimable: f.Claimable, OwnerSession: f.OwnerSession, OwnerInstance: f.OwnerInstance, Mine: f.Mine, AfterID: after, Limit: ptr(limit), Page: page}
}
func taskFilterError(err error) error {
	for _, name := range []string{"claimable", "owner_session", "owner_instance", "mine"} {
		if unknownField(err, name) {
			return ErrTaskFiltersUnsupported
		}
	}
	return err
}
func (c *Client) ListTasksFiltered(ctx context.Context, token, cid string, status comitia.TaskStatus, f comitia.TaskFilters, limit int) ([]comitia.Task, error) {
	f = f.WithInstance(c.InstanceDefaults)
	if f == (comitia.TaskFilters{}) {
		return c.ListTasks(ctx, token, cid, status, limit)
	}
	var out tasksResult
	err := c.call(ctx, token, "tasks/list", filteredTaskRequest(cid, status, f, "", limit, false), &out)
	return mapSlice(out.Tasks, func(t taskDTO) comitia.Task { return comitia.Task(t) }), taskFilterError(err)
}
func (c *Client) ListTasksPageFiltered(ctx context.Context, token, cid string, status comitia.TaskStatus, f comitia.TaskFilters, after string, limit int) (comitia.TaskPage, error) {
	f = f.WithInstance(c.InstanceDefaults)
	if f == (comitia.TaskFilters{}) {
		return c.ListTasksPage(ctx, token, cid, status, after, limit)
	}
	var out struct {
		TaskPage *taskPageDTO `json:"task_page,omitempty"`
	}
	err := c.call(ctx, token, "tasks/list", filteredTaskRequest(cid, status, f, after, limit, true), &out)
	if pagingUnsupported(err) || err == nil && out.TaskPage == nil {
		return comitia.TaskPage{}, ErrTaskFiltersUnsupported
	}
	if err != nil {
		return comitia.TaskPage{}, taskFilterError(err)
	}
	return out.TaskPage.core(), nil
}

var _ comitia.TaskFilterAPI = (*Client)(nil)

var ErrTaskOutcomesUnsupported = fmt.Errorf("%w: server does not support terminal task outcomes; upgrade the server before retrying", comitia.ErrInvalid)

func (c *Client) FailTask(ctx context.Context, token, tid string, in comitia.TaskOutcomeInput) (comitia.Task, error) {
	return c.taskOutcome(ctx, token, tid, in, "fail")
}

func (c *Client) CancelTask(ctx context.Context, token, tid string, in comitia.TaskOutcomeInput) (comitia.Task, error) {
	return c.taskOutcome(ctx, token, tid, in, "cancel")
}

func (c *Client) taskOutcome(ctx context.Context, token, tid string, in comitia.TaskOutcomeInput, action string) (comitia.Task, error) {
	var out taskResult
	err := c.call(ctx, token, "tasks/"+action, taskOutcomeRequest{tid, in.ClaimToken, ptr(in.LeaseVersion), in.Reason, in.OperationID}, &out)
	if errors.Is(err, comitia.ErrNotFound) || unknownField(err, "reason") || unknownField(err, "operation_id") {
		return comitia.Task{}, ErrTaskOutcomesUnsupported
	}
	if err = requiredResult(err, out.Task); err != nil {
		return comitia.Task{}, err
	}
	return comitia.Task(*out.Task), nil
}

var _ comitia.TaskOutcomeAPI = (*Client)(nil)
