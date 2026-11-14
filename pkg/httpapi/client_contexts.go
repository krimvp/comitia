package httpapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/krimvp/comitia/pkg/comitia"
)

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

// ErrReferenceListUnsupported reports a server that predates references/list.
var ErrReferenceListUnsupported = fmt.Errorf("%w: server does not support listing references; upgrade the server before retrying", comitia.ErrInvalid)

// ListReferences pages a context's references; see comitia.ReferenceListAPI.
// Only an unknown route answers not_found here: missing contexts are forbidden.
func (c *Client) ListReferences(ctx context.Context, token, id, after string, limit int) (comitia.ReferencePage, error) {
	var out referencePageResult
	err := c.call(ctx, token, "references/list", referencesRequest{ContextID: id, AfterID: after, Limit: &limit}, &out)
	if errors.Is(err, comitia.ErrNotFound) {
		return comitia.ReferencePage{}, ErrReferenceListUnsupported
	}
	if err = requiredResult(err, out.ReferencePage); err != nil {
		return comitia.ReferencePage{}, err
	}
	page := out.ReferencePage.core()
	if page.References == nil {
		page.References = []comitia.Reference{}
	}
	return page, nil
}

var _ comitia.ReferenceListAPI = (*Client)(nil)

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
