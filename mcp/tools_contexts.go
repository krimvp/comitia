package mcp

import (
	"context"
	"encoding/json"

	"github.com/krimvp/comitia"
)

func contextTools() []map[string]any {
	return []map[string]any{
		tool("get_context", "Get readable context metadata, including archive status. Use list_references for its references.", schema(map[string]any{"context_id": str("Context ID")}, "context_id")),
		tool("add_reference", "Add a resource reference to an active context with write permission. Stores kind (letters, digits, ._+-/) and an absolute URI with a scheme; does not fetch the resource.", schema(map[string]any{"context_id": str("Context ID"), "kind": limited("Reference kind", comitia.MaxReferenceKindBytes), "uri": limited("Resource URI", comitia.MaxReferenceURIBytes)}, "context_id", "kind", "uri")),
		tool("list_references", "List a readable context's references (archived contexts included) oldest first. URIs are unverified and may use any scheme. Pass next_after as after_id while has_more.", schema(map[string]any{"context_id": str("Context ID"), "after_id": str("Exclusive cursor from next_after"), "limit": integer("Maximum references, 1-1000 (default 100)")}, "context_id")),
		tool("archive_context", "Archive a context with grant permission or administrator status. Archived contents remain readable; writes are rejected.", schema(map[string]any{"context_id": str("Context ID")}, "context_id")),
		tool("create_context", "Create a child context with create permission on its parent. Contexts are the privacy boundary; children are private unless parent_readable grants inherited read. Top-level creation is admin/CLI only.", schema(map[string]any{"parent_context_id": str("Parent context ID"), "name": str("Child context name"), "task_id": str("Optional task in the parent"), "parent_readable": map[string]any{"type": "boolean", "description": "Allow parent readers to read the child (default false)"}}, "parent_context_id", "name")),
		tool("list_contexts", "List readable contexts with parent/task metadata, ordered by ID. Contexts are the privacy boundary; use the last returned ID as after_id, and an empty page marks the end.", schema(map[string]any{"after_id": str("Exclusive context ID cursor"), "limit": integer("Maximum contexts, 1-1000 (default 100)")})),
		tool("list_child_contexts", "List child metadata with read on the parent. Contexts are the privacy boundary: listing children does not grant read on their messages. Use the last returned ID as after_id.", schema(map[string]any{"parent_context_id": str("Parent context ID"), "after_id": str("Exclusive child ID cursor"), "limit": integer("Maximum children, 1-1000 (default 100)")}, "parent_context_id")),
	}
}

func (s *Server) callContexts(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	switch name {
	case "get_context", "archive_context":
		var a struct {
			ContextID string `json:"context_id"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("context_id", a.ContextID); err != nil {
			return nil, err
		}
		if name == "get_context" {
			return s.API.GetContext(ctx, s.Credential, a.ContextID)
		}
		if err := s.API.ArchiveContext(ctx, s.Credential, a.ContextID); err != nil {
			return nil, err
		}
		return map[string]bool{"archived": true}, nil
	case "add_reference":
		var a struct {
			ContextID string `json:"context_id"`
			Kind      string `json:"kind"`
			URI       string `json:"uri"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("context_id", a.ContextID, "kind", a.Kind, "uri", a.URI); err != nil {
			return nil, err
		}
		return s.API.AddReference(ctx, s.Credential, comitia.AddReferenceInput{ContextID: a.ContextID, Kind: a.Kind, URI: a.URI})
	case "list_references":
		var a struct {
			ContextID string `json:"context_id"`
			AfterID   string `json:"after_id"`
			Limit     *int   `json:"limit"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("context_id", a.ContextID); err != nil {
			return nil, err
		}
		limit := 100
		if a.Limit != nil {
			limit = *a.Limit
		}
		return comitia.ListReferences(ctx, s.API, s.Credential, a.ContextID, a.AfterID, limit)
	case "create_context":
		var a struct {
			ParentContextID string `json:"parent_context_id"`
			Name            string `json:"name"`
			TaskID          string `json:"task_id"`
			ParentReadable  bool   `json:"parent_readable"`
		}
		if err := decode(raw, &a); err != nil {
			return nil, err
		}
		if err := required("parent_context_id", a.ParentContextID, "name", a.Name); err != nil {
			return nil, err
		}
		return s.API.CreateChildContext(ctx, s.Credential, comitia.CreateChildContextInput{ParentContextID: a.ParentContextID, Name: a.Name, TaskID: a.TaskID, ParentReadable: a.ParentReadable})
	case "list_contexts", "list_child_contexts":
		var a struct {
			ParentContextID string `json:"parent_context_id"`
			AfterID         string `json:"after_id"`
			Limit           *int   `json:"limit"`
		}
		// A parent is only an argument to the child listing.
		if name == "list_contexts" {
			var page struct {
				AfterID string `json:"after_id"`
				Limit   *int   `json:"limit"`
			}
			if err := decode(raw, &page); err != nil {
				return nil, err
			}
			a.AfterID, a.Limit = page.AfterID, page.Limit
		} else {
			if err := decode(raw, &a); err != nil {
				return nil, err
			}
			if err := required("parent_context_id", a.ParentContextID); err != nil {
				return nil, err
			}
		}
		limit := 100
		if a.Limit != nil {
			limit = *a.Limit
		}
		if limit < 1 || limit > 1000 {
			return nil, comitia.Invalidf("limit must be between 1 and 1000")
		}
		if len(a.AfterID) > 128 {
			return nil, comitia.Invalidf("after_id must be at most 128 bytes")
		}
		var contexts []comitia.WorkContext
		var err error
		if name == "list_contexts" {
			contexts, err = s.API.ListContexts(ctx, s.Credential, a.AfterID, limit)
		} else {
			contexts, err = s.API.ListChildContexts(ctx, s.Credential, a.ParentContextID, a.AfterID, limit)
		}
		if contexts == nil {
			contexts = []comitia.WorkContext{}
		}
		return contexts, err
	default:
		return nil, errUnknownTool
	}
}
