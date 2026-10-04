package legacy

import (
	"context"
	"encoding/json"

	"github.com/krimvp/comitia"
)

func (h *handler) routePresence(ctx context.Context, token, route string, q request, out *response) (bool, error) {
	switch route {
	case "instances/list":
		var err error
		out.Instances, err = h.api.ListInstances(ctx, token, q.ContextID)
		return true, err
	case "instances/resume":
		return true, h.api.SetInstanceResume(ctx, token, q.ContextID, q.Instance, q.Resume)
	default:
		return false, nil
	}
}

func (c *Client) ListInstances(ctx context.Context, token, cid string) ([]comitia.InstancePresence, error) {
	var out response
	err := c.call(ctx, token, "instances/list", request{ContextID: cid}, &out)
	if err == nil && out.Instances == nil {
		out.Instances = []comitia.InstancePresence{}
	}
	return out.Instances, err
}
func (c *Client) SetInstanceResume(ctx context.Context, token, cid string, instance *comitia.AgentInstance, resume json.RawMessage) error {
	return c.call(ctx, token, "instances/resume", request{ContextID: cid, Instance: c.InstanceDefaults.Message(instance), Resume: resume}, &response{})
}
