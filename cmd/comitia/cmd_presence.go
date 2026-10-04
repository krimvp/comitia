package main

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/krimvp/comitia"
)

func runPresence(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	switch cmd {
	case "instances":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		if err := fs.Parse(args); err != nil {
			return err
		}
		v, err := api.ListInstances(ctx, needCredential(cfg), *cid)
		if err != nil {
			return err
		}
		return out.Encode(nonNilList(v))
	case "instance-resume":
		fs := newFlags(cmd)
		cid := fs.String("context-id", "", "context ID")
		instance := fs.String("agent-instance", "", "self-reported instance ID (defaults to COMITIA_AGENT_INSTANCE)")
		label := fs.String("agent-instance-label", "", "optional self-reported instance label (defaults to COMITIA_AGENT_INSTANCE_LABEL)")
		file := fs.String("resume-file", "", "resume metadata JSON object file (at most 4096 bytes)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		if *file == "" {
			return comitia.Invalidf("resume-file required")
		}
		input, err := os.Open(*file)
		if err != nil {
			return err
		}
		defer input.Close()
		raw, err := io.ReadAll(io.LimitReader(input, comitia.MaxInstanceResumeBytes+1))
		if err != nil {
			return err
		}
		if len(raw) > comitia.MaxInstanceResumeBytes || !json.Valid(raw) {
			return comitia.Invalidf("resume-file must contain valid JSON of at most %d bytes", comitia.MaxInstanceResumeBytes)
		}
		if err := api.SetInstanceResume(ctx, needCredential(cfg), *cid, messageInstance(cfg, fs, *instance, *label), raw); err != nil {
			return err
		}
		return out.Encode(map[string]bool{"ok": true})
	default:
		return errUnknownCommand
	}
}
