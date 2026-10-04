package httpapi

import (
	"context"
	"encoding/json"
	"github.com/krimvp/comitia"
)

func (h *handler) presenceRoutes() map[string]endpoint {
	return map[string]endpoint{
		"instances/list": typedEndpoint(func(ctx context.Context, token string, q contextRequest) (instancesResult, error) {
			v, err := h.api.ListInstances(ctx, token, q.ContextID)
			if v == nil {
				v = []comitia.InstancePresence{}
			}
			return instancesResult{v}, err
		}),
		"instances/resume": typedEndpoint(func(ctx context.Context, token string, q resumeRequest) (emptyResult, error) {
			return emptyResult{}, h.api.SetInstanceResume(ctx, token, q.ContextID, q.Instance, q.Resume)
		}),
	}
}
func (c *Client) ListInstances(ctx context.Context, token, id string) ([]comitia.InstancePresence, error) {
	var out instancesResult
	err := c.call(ctx, token, "instances/list", contextRequest{id}, &out)
	return out.Instances, err
}
func (c *Client) SetInstanceResume(ctx context.Context, token, id string, instance *comitia.AgentInstance, resume json.RawMessage) error {
	return c.call(ctx, token, "instances/resume", resumeRequest{id, c.InstanceDefaults.Message(instance), resume}, &emptyResult{})
}
