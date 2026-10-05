package httpapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/krimvp/comitia"
)

var ErrTaskOutcomesUnsupported = fmt.Errorf("%w: server does not support terminal task outcomes; upgrade the server before retrying", comitia.ErrInvalid)

type taskOutcomeRequest struct {
	TaskID       string `json:"task_id"`
	ClaimToken   string `json:"claim_token,omitempty"`
	LeaseVersion *int64 `json:"lease_version"`
	Reason       string `json:"reason,omitempty"`
	OperationID  string `json:"operation_id,omitempty"`
}

func (h *handler) taskOutcomeEndpoint(cancel bool) endpoint {
	return typedEndpoint(func(ctx context.Context, token string, q taskOutcomeRequest) (taskResult, error) {
		if q.LeaseVersion == nil {
			return taskResult{}, comitia.Invalidf("lease_version required")
		}
		in := comitia.TaskOutcomeInput{ClaimToken: q.ClaimToken, LeaseVersion: *q.LeaseVersion, Reason: q.Reason, OperationID: q.OperationID}
		call := comitia.FailTask
		if cancel {
			call = comitia.CancelTask
		}
		v, err := call(ctx, h.api, token, q.TaskID, in)
		return taskResult{ptr(taskDTO(v))}, err
	})
}

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
