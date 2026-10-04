package httpapi

import (
	"context"

	"github.com/krimvp/comitia"
)

func (h *handler) routeContexts(ctx context.Context, token, route string, q request, out *response) (found bool, err error) {
	switch route {
	case "contexts/list":
		out.Contexts, err = h.api.ListContexts(ctx, token, q.AfterID, q.Limit)
	case "workspaces/create":
		var v comitia.Workspace
		v, err = h.api.CreateWorkspace(ctx, token, q.Name)
		out.Workspace = &v
	case "contexts/create":
		if q.CreateContext == nil {
			err = comitia.ErrInvalid
			break
		}
		var v comitia.WorkContext
		v, err = h.api.CreateContext(ctx, token, *q.CreateContext)
		out.Context = &v
	case "contexts/create-child":
		if q.ChildContext == nil {
			err = comitia.ErrInvalid
			break
		}
		var v comitia.WorkContext
		v, err = h.api.CreateChildContext(ctx, token, *q.ChildContext)
		out.Context = &v
	case "contexts/children":
		out.Contexts, err = h.api.ListChildContexts(ctx, token, q.ContextID, q.AfterID, q.Limit)
	case "contexts/get":
		var v comitia.WorkContext
		v, err = h.api.GetContext(ctx, token, q.ContextID)
		out.Context = &v
	case "references/add":
		if q.Reference == nil {
			err = comitia.ErrInvalid
			break
		}
		var v comitia.Reference
		v, err = h.api.AddReference(ctx, token, *q.Reference)
		out.Reference = &v
	default:
		return false, nil
	}
	return true, err
}

func (c *Client) CreateWorkspace(x context.Context, t, n string) (comitia.Workspace, error) {
	var o response
	e := c.call(x, t, "workspaces/create", request{Name: n}, &o)
	if o.Workspace == nil {
		return comitia.Workspace{}, missing(e)
	}
	return *o.Workspace, e
}
func (c *Client) CreateContext(x context.Context, t string, i comitia.CreateContextInput) (comitia.WorkContext, error) {
	var o response
	e := c.call(x, t, "contexts/create", request{CreateContext: &i}, &o)
	if o.Context == nil {
		return comitia.WorkContext{}, missing(e)
	}
	return *o.Context, e
}
func (c *Client) CreateChildContext(x context.Context, t string, i comitia.CreateChildContextInput) (comitia.WorkContext, error) {
	var o response
	e := c.call(x, t, "contexts/create-child", request{ChildContext: &i}, &o)
	if o.Context == nil {
		return comitia.WorkContext{}, missing(e)
	}
	return *o.Context, e
}
func (c *Client) ListChildContexts(ctx context.Context, token, parentID, afterID string, limit int) ([]comitia.WorkContext, error) {
	var out response
	err := c.call(ctx, token, "contexts/children", request{ContextID: parentID, AfterID: afterID, Limit: limit}, &out)
	return out.Contexts, err
}
func (c *Client) GetContext(x context.Context, t, id string) (comitia.WorkContext, error) {
	var o response
	e := c.call(x, t, "contexts/get", request{ContextID: id}, &o)
	if o.Context == nil {
		return comitia.WorkContext{}, missing(e)
	}
	return *o.Context, e
}
func (c *Client) AddReference(x context.Context, t string, i comitia.AddReferenceInput) (comitia.Reference, error) {
	var o response
	e := c.call(x, t, "references/add", request{Reference: &i}, &o)
	if o.Reference == nil {
		return comitia.Reference{}, missing(e)
	}
	return *o.Reference, e
}
func (c *Client) ListContexts(ctx context.Context, token, afterID string, limit int) ([]comitia.WorkContext, error) {
	var out response
	err := c.call(ctx, token, "contexts/list", request{AfterID: afterID, Limit: limit}, &out)
	return out.Contexts, err
}
