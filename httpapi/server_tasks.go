package httpapi

import (
	"context"

	"github.com/krimvp/comitia"
)

func (h *handler) taskRoutes() map[string]endpoint {
	return map[string]endpoint{
		"tasks/fail":   h.taskOutcomeEndpoint(false),
		"tasks/cancel": h.taskOutcomeEndpoint(true),
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
				v, err := comitia.ListTaskPageFiltered(ctx, h.api, token, q.ContextID, q.Status, q.filters(), q.AfterID, limit)
				if err != nil {
					return listTasksResult{}, err
				}
				return listTasksResult{TaskPage: ptr(wireTaskPage(v))}, nil
			}
			v, err := comitia.ListTasksFiltered(ctx, h.api, token, q.ContextID, q.Status, q.filters(), limit)
			return listTasksResult{Tasks: ptr(mapSlice(v, func(t comitia.Task) taskDTO { return taskDTO(t) }))}, err
		}),
		"tasks/claim": typedEndpoint(func(ctx context.Context, token string, q claimRequest) (claimResult, error) {
			lease, err := durationSeconds(q.LeaseSeconds, q.TTL, comitia.DefaultLease, q.OpenEnded)
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
		"tasks/recover": typedEndpoint(func(ctx context.Context, token string, q recoverClaimRequest) (claimResult, error) {
			if q.LeaseVersion == nil {
				return claimResult{}, comitia.Invalidf("lease_version required")
			}
			var instances []comitia.AgentInstance
			if q.ClaimInstance != nil {
				instances = append(instances, *q.ClaimInstance)
			}
			v, err := comitia.RecoverClaim(ctx, h.api, token, q.TaskID, *q.LeaseVersion, instances...)
			return claimResult{ptr(wireClaim(v))}, err
		}),
		"tasks/renew": typedEndpoint(func(ctx context.Context, token string, q renewRequest) (claimResult, error) {
			if q.LeaseVersion == nil {
				return claimResult{}, comitia.Invalidf("lease_version required")
			}
			lease, err := durationSeconds(q.LeaseSeconds, q.TTL, comitia.DefaultLease, q.OpenEnded)
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

func (q listTasksRequest) filters() comitia.TaskFilters {
	return comitia.TaskFilters{Claimable: q.Claimable, OwnerSession: q.OwnerSession, OwnerInstance: q.OwnerInstance, Mine: q.Mine}
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
