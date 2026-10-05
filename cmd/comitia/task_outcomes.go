package main

import (
	"context"
	"encoding/json"

	"github.com/krimvp/comitia"
)

func runTaskOutcome(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	fs := newFlags(cmd)
	tid := fs.String("task-id", "", "task ID")
	token := fs.String("claim-token", "", "current claim token; optional for admin cancellation")
	version := fs.Int64("lease-version", -1, "current version, including 0 for pending tasks")
	reason := fs.String("reason", "", "terminal reason; do not include secrets")
	operationID := fs.String("operation-id", "", "stable session-scoped mutation ID for lost-response recovery")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if cfg.parseOnly {
		return nil
	}
	if *tid == "" || *version < 0 {
		return comitia.Invalidf("task-id and nonnegative lease-version required")
	}
	in := comitia.TaskOutcomeInput{ClaimToken: *token, LeaseVersion: *version, Reason: *reason, OperationID: *operationID}
	call := comitia.FailTask
	if cmd == "task-cancel" {
		call = comitia.CancelTask
	}
	v, err := call(ctx, api, needCredential(cfg), *tid, in)
	if err != nil {
		return err
	}
	return out.Encode(v)
}
