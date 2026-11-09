package main

import (
	"context"
	"encoding/json"

	"github.com/krimvp/comitia"
)

func runContexts(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	switch cmd {
	case "context-get":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID (needs read permission)")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		if *cid == "" {
			return comitia.Invalidf("context-get requires --context-id")
		}
		v, err := api.GetContext(ctx, needCredential(cfg), *cid)
		if err != nil {
			return err
		}
		return out.Encode(v)
	case "context-archive":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID to archive (needs grant or administrator status)")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		if *cid == "" {
			return comitia.Invalidf("context-archive requires --context-id")
		}
		if err := api.ArchiveContext(ctx, needCredential(cfg), *cid); err != nil {
			return err
		}
		return out.Encode(map[string]bool{"archived": true})
	case "workspace-create":
		fs := newFlags(cmd)
		name := fs.String("name", "", "workspace name")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
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
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
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
		wid := fs.String("workspace-id", "", "workspace ID (top-level contexts, admins only)")
		name := fs.String("name", "", "context name")
		parent := fs.String("parent-id", "", "parent context ID; creates a child context (needs create on the parent)")
		task := fs.String("task-id", "", "task in the parent context that the child serves")
		readable := fs.Bool("parent-readable", false, "let sessions with read on the parent read the child")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		var v comitia.WorkContext
		var e error
		if *parent != "" {
			if *wid != "" {
				return comitia.Invalidf("context-create: use either --workspace-id or --parent-id")
			}
			v, e = api.CreateChildContext(ctx, needCredential(cfg), comitia.CreateChildContextInput{ParentContextID: *parent, Name: *name, TaskID: *task, ParentReadable: *readable})
		} else {
			if *task != "" || *readable {
				return comitia.Invalidf("context-create: --task-id and --parent-readable need --parent-id")
			}
			v, e = api.CreateContext(ctx, needCredential(cfg), comitia.CreateContextInput{WorkspaceID: *wid, Name: *name})
		}
		if e != nil {
			return e
		}
		return out.Encode(v)
	case "context-children":
		fs := newFlags(cmd)
		parent := fs.String("parent-id", "", "parent context ID")
		after := fs.String("after-id", "", "exclusive context ID cursor")
		limit := fs.Int("limit", 100, "maximum contexts (1-1000)")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
		}
		v, e := api.ListChildContexts(ctx, needCredential(cfg), *parent, *after, *limit)
		if e != nil {
			return e
		}
		if v == nil {
			v = []comitia.WorkContext{}
		}
		return out.Encode(v)
	case "reference-add":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		kind := fs.String("kind", "", "reference kind")
		uri := fs.String("uri", "", "reference URI")
		if err := parseFlags(fs, args); err != nil {
			return err
		}
		if cfg.parseOnly {
			return nil
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
