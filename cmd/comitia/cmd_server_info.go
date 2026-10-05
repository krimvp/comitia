package main

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/krimvp/comitia"
)

func runServerInfo(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	if cmd != "server-info" && cmd != "storage-status" {
		return errUnknownCommand
	}
	if err := newFlags(cmd).Parse(args); err != nil {
		return err
	}
	if cfg.parseOnly {
		return nil
	}
	if cmd == "storage-status" {
		storage, ok := api.(comitia.StorageStatusAPI)
		if !ok {
			return comitia.Invalidf("storage-status unsupported by this server")
		}
		status, err := storage.StorageStatus(ctx, needCredential(cfg))
		if errors.Is(err, comitia.ErrNotFound) {
			return comitia.Invalidf("storage-status unsupported by this server")
		}
		if err != nil {
			return err
		}
		return out.Encode(status)
	}
	info, err := api.ServerInfo(ctx, needCredential(cfg))
	if err != nil {
		return err
	}
	return out.Encode(info)
}
