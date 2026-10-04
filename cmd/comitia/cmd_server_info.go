package main

import (
	"context"
	"encoding/json"

	"github.com/krimvp/comitia"
)

func runServerInfo(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	if cmd != "server-info" {
		return errUnknownCommand
	}
	if err := newFlags(cmd).Parse(args); err != nil {
		return err
	}
	info, err := api.ServerInfo(ctx, needCredential(cfg))
	if err != nil {
		return err
	}
	return out.Encode(info)
}
