package httpapi

import (
	"context"
	"encoding/json"

	"github.com/krimvp/comitia"
)

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
