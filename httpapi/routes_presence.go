package httpapi

import (
	"context"
	"encoding/json"
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

func (c *Client) ListInstances(ctx context.Context, token, id string) ([]comitia.InstancePresence, error) {
	var out instancesResult
	err := c.call(ctx, token, "instances/list", contextRequest{id}, &out)
	return out.Instances, err
}

// ListInstancesPage falls back to the legacy list when the server predates
// instance paging.
func (c *Client) ListInstancesPage(ctx context.Context, token, id, after string, limit int) (comitia.InstancePage, error) {
	var out struct {
		InstancePage *comitia.InstancePage `json:"instance_page,omitempty"`
	}
	err := c.call(ctx, token, "instances/list", instancesRequest{ContextID: id, Page: true, AfterID: after, Limit: &limit}, &out)
	if pagingUnsupported(err) || unknownField(err, "limit") || err == nil && out.InstancePage == nil {
		return comitia.ListInstancePage(ctx, legacyInstanceLists{c}, token, id, after, limit)
	}
	if err != nil {
		return comitia.InstancePage{}, err
	}
	if out.InstancePage.Instances == nil {
		out.InstancePage.Instances = []comitia.InstancePresence{}
	}
	return *out.InstancePage, nil
}

// legacyInstanceLists hides the Client's paging capability from the fallback.
type legacyInstanceLists struct{ comitia.PresenceAPI }

func (c *Client) SetInstanceResume(ctx context.Context, token, id string, instance *comitia.AgentInstance, resume json.RawMessage) error {
	return c.call(ctx, token, "instances/resume", resumeRequest{id, c.InstanceDefaults.Message(instance), resume}, &emptyResult{})
}
