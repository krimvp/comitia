// The fail_task and cancel_task tools: their shared schema and handler.

package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/krimvp/comitia"
)

func taskOutcomeSchema(cancel bool) map[string]any {
	fields := map[string]any{"task_id": str("Task ID"), "claim_token": str("Current claim token; optional for admin cancellation"), "lease_version": integer("Current version, including 0 for pending tasks"), "reason": str(fmt.Sprintf("Terminal reason, at most %d UTF-8 bytes; do not include secrets", comitia.MaxTaskReasonBytes)), "operation_id": taskOperationID()}
	if cancel {
		return schema(fields, "task_id", "lease_version", "reason")
	}
	return schema(fields, "task_id", "claim_token", "lease_version", "reason")
}

func (s *Server) callTaskOutcome(ctx context.Context, name string, raw json.RawMessage) (any, error) {
	var a struct {
		TaskID       string `json:"task_id"`
		ClaimToken   string `json:"claim_token"`
		LeaseVersion *int64 `json:"lease_version"`
		Reason       string `json:"reason"`
		OperationID  string `json:"operation_id"`
	}
	if err := decode(raw, &a); err != nil {
		return nil, err
	}
	if err := required("task_id", a.TaskID, "reason", a.Reason); err != nil {
		return nil, err
	}
	if a.LeaseVersion == nil {
		return nil, comitia.Invalidf("lease_version required")
	}
	if name == "fail_task" && a.ClaimToken == "" {
		return nil, comitia.Invalidf("claim_token required")
	}
	in := comitia.TaskOutcomeInput{ClaimToken: a.ClaimToken, LeaseVersion: *a.LeaseVersion, Reason: a.Reason, OperationID: a.OperationID}
	if name == "cancel_task" {
		return comitia.CancelTask(ctx, s.API, s.Credential, a.TaskID, in)
	}
	return comitia.FailTask(ctx, s.API, s.Credential, a.TaskID, in)
}
