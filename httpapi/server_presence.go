package httpapi

import (
	"context"

	"github.com/krimvp/comitia"
)

func (h *handler) presenceRoutes() map[string]endpoint {
	return map[string]endpoint{
		"instances/list": typedEndpoint(func(ctx context.Context, token string, q instancesRequest) (listInstancesResult, error) {
			if q.Page || q.AfterID != "" {
				limit, err := listLimit(q.Limit)
				if err != nil {
					return listInstancesResult{}, err
				}
				v, err := comitia.ListInstancePage(ctx, h.api, token, q.ContextID, q.AfterID, limit)
				if err != nil {
					return listInstancesResult{}, err
				}
				return listInstancesResult{InstancePage: &v}, nil
			}
			v, err := listInstances(ctx, h.api, token, q.ContextID, q.Limit)
			if v == nil {
				v = []comitia.InstancePresence{}
			}
			return listInstancesResult{Instances: &v}, err
		}),
		"instances/resume": typedEndpoint(func(ctx context.Context, token string, q resumeRequest) (emptyResult, error) {
			return emptyResult{}, h.api.SetInstanceResume(ctx, token, q.ContextID, q.Instance, q.Resume)
		}),
	}
}

// An unpaged list keeps the default bound unless the request names a limit.
func listInstances(ctx context.Context, api comitia.PresenceAPI, token, cid string, limit *int) ([]comitia.InstancePresence, error) {
	if limit == nil {
		return api.ListInstances(ctx, token, cid)
	}
	n, err := listLimit(limit)
	if err != nil {
		return nil, err
	}
	if pages, ok := api.(comitia.InstancePageAPI); ok {
		v, err := pages.ListInstancesPage(ctx, token, cid, "", n)
		return v.Instances, err
	}
	v, err := api.ListInstances(ctx, token, cid)
	if len(v) > n {
		v = v[:n]
	}
	return v, err
}
