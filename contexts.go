package comitia

import (
	"context"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/service"
)

// Workspaces, work contexts and their references.

type Workspace = model.Workspace

type WorkContext = model.WorkContext

type Reference = model.Reference

type CreateContextInput = model.CreateContextInput

type CreateChildContextInput = model.CreateChildContextInput

type AddReferenceInput = model.AddReferenceInput

type ContextAPI = model.ContextAPI

// ReferencePage lists a context's references by creation time and ID.
type ReferencePage = model.ReferencePage

// Reference listing is optional: implementations of the original API/store
// contracts remain valid.
type ReferenceListAPI = model.ReferenceListAPI

// ListReferences selects reference listing without changing ContextAPI.
func ListReferences(ctx context.Context, api ContextAPI, token, cid, after string, limit int) (ReferencePage, error) {
	return service.ListReferences(ctx, api, token, cid, after, limit)
}
