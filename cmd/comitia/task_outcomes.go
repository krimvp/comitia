// The terminal task commands task-fail and task-cancel, routed from runTasks.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/krimvp/comitia/pkg/comitia"
)

func runTaskOutcome(ctx context.Context, cfg config, api comitia.API, out *json.Encoder, cmd string, args []string) error {
	fs := newFlags(cmd)
	tid := fs.String("task-id", "", "task ID")
	token := fs.String("claim-token", "", "current claim token; optional for admin cancellation")
	version := fs.Int64("lease-version", -1, "current version, including 0 for pending tasks")
	reason := fs.String("reason", "", "terminal reason; do not include secrets")
	operationID := fs.String("operation-id", "", "stable session-scoped mutation ID for lost-response recovery")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	required := []string{"task-id", "lease-version", "reason"}
	if cmd == "task-fail" {
		required = append(required, "claim-token")
	}
	if err := requireFlags(fs, required...); err != nil {
		return err
	}
	if *version < 0 {
		return comitia.Invalidf("%s requires a nonnegative --lease-version", cmd)
	}
	if cfg.parseOnly {
		return nil
	}
	in := comitia.TaskOutcomeInput{ClaimToken: *token, LeaseVersion: *version, Reason: *reason, OperationID: *operationID}
	call := comitia.FailTask
	if cmd == "task-cancel" {
		call = comitia.CancelTask
	}
	v, err := call(ctx, api, needCredential(cfg), *tid, in)
	if errors.Is(err, comitia.ErrStaleClaim) && *token == "" {
		// Only the server knows whether the caller is a context admin.
		return fmt.Errorf("%w; without context-admin authority %s requires --claim-token", err, cmd)
	}
	if err != nil {
		return err
	}
	return out.Encode(v)
}
