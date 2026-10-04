package httpapi

import (
	"context"
	"time"

	"github.com/krimvp/comitia"
)

func (h *handler) routeTasks(ctx context.Context, token, route string, q request, out *response) (found bool, err error) {
	switch route {
	case "tasks/create":
		if q.Task == nil {
			err = comitia.ErrInvalid
			break
		}
		var v comitia.Task
		v, err = h.api.CreateTask(ctx, token, q.ContextID, *q.Task)
		out.Task = &v
	case "tasks/get":
		var v comitia.Task
		v, err = h.api.GetTask(ctx, token, q.TaskID)
		out.Task = &v
	case "tasks/list":
		out.Tasks, err = h.api.ListTasks(ctx, token, q.ContextID, q.Status, q.Limit)
	case "tasks/claim":
		var v comitia.TaskClaim
		var instance []comitia.AgentInstance
		if q.ClaimInstance != nil {
			instance = append(instance, *q.ClaimInstance)
		}
		v, err = h.api.ClaimTask(ctx, token, q.TaskID, q.TTL, instance...)
		out.Claim = &v
	case "tasks/renew":
		var v comitia.TaskClaim
		v, err = h.api.RenewClaim(ctx, token, q.TaskID, q.ClaimToken, q.LeaseVersion, q.TTL)
		out.Claim = &v
	case "tasks/complete":
		var v comitia.Task
		v, err = h.api.CompleteTask(ctx, token, q.TaskID, q.ClaimToken, q.LeaseVersion)
		out.Task = &v
	case "tasks/release":
		var v comitia.Task
		v, err = h.api.ReleaseTask(ctx, token, q.TaskID, q.ClaimToken, q.LeaseVersion)
		out.Task = &v
	default:
		return false, nil
	}
	return true, err
}

func (c *Client) CreateTask(x context.Context, t, id string, i comitia.TaskInput) (comitia.Task, error) {
	var o response
	e := c.call(x, t, "tasks/create", request{ContextID: id, Task: &i}, &o)
	if o.Task == nil {
		return comitia.Task{}, missing(e)
	}
	return *o.Task, e
}
func (c *Client) GetTask(x context.Context, t, id string) (comitia.Task, error) {
	var o response
	e := c.call(x, t, "tasks/get", request{TaskID: id}, &o)
	if o.Task == nil {
		return comitia.Task{}, missing(e)
	}
	return *o.Task, e
}
func (c *Client) ListTasks(x context.Context, t, id string, status comitia.TaskStatus, limit int) ([]comitia.Task, error) {
	var o response
	e := c.call(x, t, "tasks/list", request{ContextID: id, Status: status, Limit: limit}, &o)
	return o.Tasks, e
}
func (c *Client) ClaimTask(x context.Context, t, id string, d time.Duration, instance ...comitia.AgentInstance) (comitia.TaskClaim, error) {
	var o response
	q := request{TaskID: id, TTL: d}
	switch len(instance) {
	case 0:
	case 1:
		q.ClaimInstance = &instance[0]
	default:
		return comitia.TaskClaim{}, comitia.ErrInvalid
	}
	e := c.call(x, t, "tasks/claim", q, &o)
	if o.Claim == nil {
		return comitia.TaskClaim{}, missing(e)
	}
	return *o.Claim, e
}
func (c *Client) RenewClaim(x context.Context, t, id, k string, v int64, d time.Duration) (comitia.TaskClaim, error) {
	var o response
	e := c.call(x, t, "tasks/renew", request{TaskID: id, ClaimToken: k, LeaseVersion: v, TTL: d}, &o)
	if o.Claim == nil {
		return comitia.TaskClaim{}, missing(e)
	}
	return *o.Claim, e
}
func (c *Client) CompleteTask(x context.Context, t, id, k string, v int64) (comitia.Task, error) {
	var o response
	e := c.call(x, t, "tasks/complete", request{TaskID: id, ClaimToken: k, LeaseVersion: v}, &o)
	if o.Task == nil {
		return comitia.Task{}, missing(e)
	}
	return *o.Task, e
}
func (c *Client) ReleaseTask(x context.Context, t, id, k string, v int64) (comitia.Task, error) {
	var o response
	e := c.call(x, t, "tasks/release", request{TaskID: id, ClaimToken: k, LeaseVersion: v}, &o)
	if o.Task == nil {
		return comitia.Task{}, missing(e)
	}
	return *o.Task, e
}
