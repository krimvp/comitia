package httpapi

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/krimvp/comitia"
)

// Servers before task mutation receipts reject operation_id while decoding, so
// the request was not applied. Clients must not retry it without idempotency.
var ErrTaskMutationReceiptsUnsupported = fmt.Errorf("%w: server does not support task mutation receipts (operation_id); upgrade the server before retrying", comitia.ErrInvalid)

func (h *handler) taskRoutes() map[string]endpoint {
	return map[string]endpoint{
		"tasks/create": typedEndpoint(func(ctx context.Context, token string, q createTaskRequest) (taskResult, error) {
			if q.Task == nil {
				return taskResult{}, comitia.Invalidf("task required")
			}
			v, err := comitia.CreateTaskOperation(ctx, h.api, token, q.ContextID, comitia.TaskInput(*q.Task))
			return taskResult{ptr(taskDTO(v))}, err
		}),
		"tasks/get": typedEndpoint(func(ctx context.Context, token string, q taskRequest) (taskResult, error) {
			v, err := h.api.GetTask(ctx, token, q.TaskID)
			return taskResult{ptr(taskDTO(v))}, err
		}),
		"tasks/list": typedEndpoint(func(ctx context.Context, token string, q listTasksRequest) (listTasksResult, error) {
			limit, err := listLimit(q.Limit)
			if err != nil {
				return listTasksResult{}, err
			}
			if q.Page || q.AfterID != "" {
				v, err := comitia.ListTaskPage(ctx, h.api, token, q.ContextID, q.Status, q.AfterID, limit)
				if err != nil {
					return listTasksResult{}, err
				}
				return listTasksResult{TaskPage: ptr(wireTaskPage(v))}, nil
			}
			v, err := h.api.ListTasks(ctx, token, q.ContextID, q.Status, limit)
			return listTasksResult{Tasks: ptr(mapSlice(v, func(t comitia.Task) taskDTO { return taskDTO(t) }))}, err
		}),
		"tasks/claim": typedEndpoint(func(ctx context.Context, token string, q claimRequest) (claimResult, error) {
			lease, err := durationSeconds(q.LeaseSeconds, q.TTL, 300*time.Second, q.OpenEnded)
			if err != nil {
				return claimResult{}, err
			}
			var instances []comitia.AgentInstance
			if q.ClaimInstance != nil {
				instances = append(instances, *q.ClaimInstance)
			}
			v, err := comitia.ClaimTaskOperation(ctx, h.api, token, q.TaskID, lease, q.OperationID, instances...)
			return claimResult{ptr(wireClaim(v))}, err
		}),
		"tasks/renew": typedEndpoint(func(ctx context.Context, token string, q renewRequest) (claimResult, error) {
			if q.LeaseVersion == nil {
				return claimResult{}, comitia.Invalidf("lease_version required")
			}
			lease, err := durationSeconds(q.LeaseSeconds, q.TTL, 300*time.Second, q.OpenEnded)
			if err != nil {
				return claimResult{}, err
			}
			v, err := comitia.RenewClaimOperation(ctx, h.api, token, q.TaskID, q.ClaimToken, *q.LeaseVersion, lease, q.OperationID)
			return claimResult{ptr(wireClaim(v))}, err
		}),
		"tasks/complete": typedEndpoint(func(ctx context.Context, token string, q finishTaskRequest) (taskResult, error) {
			if q.LeaseVersion == nil {
				return taskResult{}, comitia.Invalidf("lease_version required")
			}
			v, err := h.api.CompleteTask(ctx, token, q.TaskID, q.ClaimToken, *q.LeaseVersion)
			return taskResult{ptr(taskDTO(v))}, err
		}),
		"tasks/release": typedEndpoint(func(ctx context.Context, token string, q finishTaskRequest) (taskResult, error) {
			if q.LeaseVersion == nil {
				return taskResult{}, comitia.Invalidf("lease_version required")
			}
			v, err := h.api.ReleaseTask(ctx, token, q.TaskID, q.ClaimToken, *q.LeaseVersion)
			return taskResult{ptr(taskDTO(v))}, err
		}),
	}
}

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

// unknownField reports a strict decoder rejection, from either an endpoint-DTO
// Handler or the older request-union Handler, of a member it predates.
func unknownField(err error, name string) bool {
	return errors.Is(err, comitia.ErrInvalid) && strings.Contains(err.Error(), `unknown field "`+name+`"`)
}

var _ comitia.TaskMutationAPI = (*Client)(nil)
