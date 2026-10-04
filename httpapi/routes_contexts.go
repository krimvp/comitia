package httpapi

import (
	"context"
	"github.com/krimvp/comitia"
)

func (h *handler) contextRoutes() map[string]endpoint {
	return map[string]endpoint{
		"workspaces/create": typedEndpoint(func(ctx context.Context, token string, q nameRequest) (workspaceResult, error) {

			v, err := h.api.CreateWorkspace(ctx, token, q.Name)
			return workspaceResult{ptr(workspaceDTO(v))}, err
		}),
		"contexts/create": typedEndpoint(func(ctx context.Context, token string, q createContextRequest) (contextResult, error) {
			if q.CreateContext == nil {
				return contextResult{}, comitia.Invalidf("create_context required")
			}
			v, err := h.api.CreateContext(ctx, token, comitia.CreateContextInput(*q.CreateContext))
			return contextResult{ptr(contextDTO(v))}, err
		}),
		"contexts/create-child": typedEndpoint(func(ctx context.Context, token string, q childContextRequest) (contextResult, error) {
			if q.ChildContext == nil {
				return contextResult{}, comitia.Invalidf("child_context required")
			}
			v, err := h.api.CreateChildContext(ctx, token, comitia.CreateChildContextInput(*q.ChildContext))
			return contextResult{ptr(contextDTO(v))}, err
		}),
		"contexts/get": typedEndpoint(func(ctx context.Context, token string, q contextRequest) (contextResult, error) {

			v, err := h.api.GetContext(ctx, token, q.ContextID)
			return contextResult{ptr(contextDTO(v))}, err
		}),
		"references/add": typedEndpoint(func(ctx context.Context, token string, q referenceRequest) (referenceResult, error) {
			if q.Reference == nil {
				return referenceResult{}, comitia.Invalidf("reference required")
			}
			v, err := h.api.AddReference(ctx, token, comitia.AddReferenceInput(*q.Reference))
			return referenceResult{ptr(referenceDTO(v))}, err
		}),
		"contexts/list": typedEndpoint(func(ctx context.Context, token string, q contextsRequest) (contextsResult, error) {
			limit, err := listLimit(q.Limit)
			if err != nil {
				return contextsResult{}, err
			}
			v, err := h.api.ListContexts(ctx, token, q.AfterID, limit)
			return contextsResult{mapSlice(v, func(c comitia.WorkContext) contextDTO { return contextDTO(c) })}, err
		}),
		"contexts/children": typedEndpoint(func(ctx context.Context, token string, q childrenRequest) (contextsResult, error) {
			limit, err := listLimit(q.Limit)
			if err != nil {
				return contextsResult{}, err
			}
			v, err := h.api.ListChildContexts(ctx, token, q.ContextID, q.AfterID, limit)
			return contextsResult{mapSlice(v, func(c comitia.WorkContext) contextDTO { return contextDTO(c) })}, err
		}),
		"contexts/archive": typedEndpoint(func(ctx context.Context, token string, q contextRequest) (emptyResult, error) {
			return emptyResult{}, h.api.ArchiveContext(ctx, token, q.ContextID)
		}),
	}
}
func (c *Client) CreateWorkspace(ctx context.Context, token string, name string) (comitia.Workspace, error) {
	var out workspaceResult
	err := c.call(ctx, token, "workspaces/create", nameRequest{Name: name}, &out)
	if err = requiredResult(err, out.Workspace); err != nil {
		return comitia.Workspace{}, err
	}
	return comitia.Workspace(*out.Workspace), nil
}
func (c *Client) CreateContext(ctx context.Context, token string, input comitia.CreateContextInput) (comitia.WorkContext, error) {
	var out contextResult
	err := c.call(ctx, token, "contexts/create", createContextRequest{ptr(createContextInput(input))}, &out)
	if err = requiredResult(err, out.Context); err != nil {
		return comitia.WorkContext{}, err
	}
	return comitia.WorkContext(*out.Context), nil
}
func (c *Client) CreateChildContext(ctx context.Context, token string, input comitia.CreateChildContextInput) (comitia.WorkContext, error) {
	var out contextResult
	err := c.call(ctx, token, "contexts/create-child", childContextRequest{ptr(childContextInput(input))}, &out)
	if err = requiredResult(err, out.Context); err != nil {
		return comitia.WorkContext{}, err
	}
	return comitia.WorkContext(*out.Context), nil
}
func (c *Client) GetContext(ctx context.Context, token string, id string) (comitia.WorkContext, error) {
	var out contextResult
	err := c.call(ctx, token, "contexts/get", contextRequest{id}, &out)
	if err = requiredResult(err, out.Context); err != nil {
		return comitia.WorkContext{}, err
	}
	return comitia.WorkContext(*out.Context), nil
}
func (c *Client) AddReference(ctx context.Context, token string, input comitia.AddReferenceInput) (comitia.Reference, error) {
	var out referenceResult
	err := c.call(ctx, token, "references/add", referenceRequest{ptr(referenceInput(input))}, &out)
	if err = requiredResult(err, out.Reference); err != nil {
		return comitia.Reference{}, err
	}
	return comitia.Reference(*out.Reference), nil
}
func (c *Client) ListContexts(ctx context.Context, token string, after string, limit int) ([]comitia.WorkContext, error) {
	var out contextsResult
	err := c.call(ctx, token, "contexts/list", contextsRequest{AfterID: after, Limit: ptr(limit)}, &out)
	return mapSlice(out.Contexts, func(v contextDTO) comitia.WorkContext { return comitia.WorkContext(v) }), err
}
func (c *Client) ListChildContexts(ctx context.Context, token string, parent, after string, limit int) ([]comitia.WorkContext, error) {
	var out contextsResult
	err := c.call(ctx, token, "contexts/children", childrenRequest{ContextID: parent, AfterID: after, Limit: ptr(limit)}, &out)
	return mapSlice(out.Contexts, func(v contextDTO) comitia.WorkContext { return comitia.WorkContext(v) }), err
}
func (c *Client) ArchiveContext(ctx context.Context, token, id string) error {
	return c.call(ctx, token, "contexts/archive", contextRequest{id}, &emptyResult{})
}
