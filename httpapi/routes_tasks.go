package httpapi

import (
	"context"
	"github.com/krimvp/comitia"
	"time"
)

func (h *handler) taskRoutes() map[string]endpoint {
	return map[string]endpoint{
		"tasks/create": typedEndpoint(func(ctx context.Context, token string, q createTaskRequest) (taskResult, error) {
			if q.Task == nil {
				return taskResult{}, comitia.Invalidf("task required")
			}
			v, err := h.api.CreateTask(ctx, token, q.ContextID, comitia.TaskInput(*q.Task))
			return taskResult{ptr(taskDTO(v))}, err
		}),
		"tasks/get": typedEndpoint(func(ctx context.Context, token string, q taskRequest) (taskResult, error) {
			v, err := h.api.GetTask(ctx, token, q.TaskID)
			return taskResult{ptr(taskDTO(v))}, err
		}),
		"tasks/list": typedEndpoint(func(ctx context.Context, token string, q listTasksRequest) (tasksResult, error) {
			limit, err := listLimit(q.Limit)
			if err != nil {
				return tasksResult{}, err
			}
			v, err := h.api.ListTasks(ctx, token, q.ContextID, q.Status, limit)
			return tasksResult{mapSlice(v, func(t comitia.Task) taskDTO { return taskDTO(t) })}, err
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
			v, err := h.api.ClaimTask(ctx, token, q.TaskID, lease, instances...)
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
			v, err := h.api.RenewClaim(ctx, token, q.TaskID, q.ClaimToken, *q.LeaseVersion, lease)
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
	err := c.call(ctx, token, "tasks/list", listTasksRequest{id, status, ptr(limit)}, &out)
	return mapSlice(out.Tasks, func(t taskDTO) comitia.Task { return comitia.Task(t) }), err
}
func (c *Client) ClaimTask(ctx context.Context, token, id string, lease time.Duration, instances ...comitia.AgentInstance) (comitia.TaskClaim, error) {
	q := claimRequest{TaskID: id, TTL: ptr(lease)}
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
	if err = requiredResult(err, out.Claim); err != nil {
		return comitia.TaskClaim{}, err
	}
	return out.Claim.core(), nil
}
func (c *Client) RenewClaim(ctx context.Context, token, id, key string, version int64, lease time.Duration) (comitia.TaskClaim, error) {
	var out claimResult
	err := c.call(ctx, token, "tasks/renew", renewRequest{TaskID: id, ClaimToken: key, LeaseVersion: ptr(version), TTL: ptr(lease)}, &out)
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
