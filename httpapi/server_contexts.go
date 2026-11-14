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
		"references/list": typedEndpoint(func(ctx context.Context, token string, q referencesRequest) (referencePageResult, error) {
			limit, err := listLimit(q.Limit)
			if err != nil {
				return referencePageResult{}, err
			}
			v, err := comitia.ListReferences(ctx, h.api, token, q.ContextID, q.AfterID, limit)
			if err != nil {
				return referencePageResult{}, err
			}
			return referencePageResult{ptr(wireReferencePage(v))}, nil
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
