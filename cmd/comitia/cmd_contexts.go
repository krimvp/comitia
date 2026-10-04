package main

import (
	"context"
	"encoding/json"

	"github.com/krimvp/comitia"
)

func runContexts(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	switch cmd {
	case "workspace-create":
		fs := newFlags(cmd)
		name := fs.String("name", "", "workspace name")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.CreateWorkspace(ctx, needCredential(cfg), *name)
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "context-list":
		fs := newFlags(cmd)
		after := fs.String("after-id", "", "exclusive context ID cursor")
		limit := fs.Int("limit", 100, "maximum contexts (1-1000)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.ListContexts(ctx, needCredential(cfg), *after, *limit)
		if e != nil {
			return e
		}
		if v == nil {
			v = []comitia.WorkContext{}
		}
		return out.Encode(v)
	case "context-create":
		fs := newFlags(cmd)
		wid := fs.String("workspace-id", "", "workspace ID")
		name := fs.String("name", "", "context name")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.CreateContext(ctx, needCredential(cfg), comitia.CreateContextInput{WorkspaceID: *wid, Name: *name})
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "reference-add":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		kind := fs.String("kind", "", "reference kind")
		uri := fs.String("uri", "", "reference URI")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, e := api.AddReference(ctx, needCredential(cfg), comitia.AddReferenceInput{ContextID: *cid, Kind: *kind, URI: *uri})
		if e != nil {
			return e
		}
		return out.Encode(v)
	default:
		return errUnknownCommand
	}
}
