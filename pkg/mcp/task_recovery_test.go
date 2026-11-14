package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

func TestMCPTaskRecoveryAndFence(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			s, _, _, c := contextServer(t, remote)
			ctx := context.Background()
			call := func(name string, args map[string]any) any {
				t.Helper()
				raw, err := json.Marshal(args)
				if err != nil {
					t.Fatal(err)
				}
				out, err := s.call(ctx, name, raw)
				if err != nil {
					t.Fatal(err)
				}
				return out
			}
			create := map[string]any{"context_id": c.ID, "title": "job", "operation_id": "create"}
			task := call("create_task", create).(comitia.Task)
			if again := call("create_task", create).(comitia.Task); !reflect.DeepEqual(task, again) {
				t.Fatalf("create replay: %+v", again)
			}
			args := map[string]any{"task_id": task.ID, "operation_id": "claim", "agent_instance": map[string]any{"id": "worker"}}
			claim := call("claim_task", args).(comitia.TaskClaim)
			again := call("claim_task", args).(comitia.TaskClaim)
			if !reflect.DeepEqual(claim.Task, again.Task) || again.Token == "" || again.Token == claim.Token {
				t.Fatal("claim retry must rotate only token")
			}
			claim = again
			args = map[string]any{"task_id": task.ID, "operation_id": "renew", "claim_token": claim.Token, "lease_version": claim.Task.LeaseVersion}
			renewed := call("renew_task", args).(comitia.TaskClaim)
			again = call("renew_task", args).(comitia.TaskClaim)
			if !reflect.DeepEqual(renewed.Task, again.Task) || again.Token == "" || again.Token == renewed.Token {
				t.Fatal("renew retry must rotate only token")
			}
			renewed = again
			result := map[string]any{"context_id": c.ID, "dedupe_key": "result", "body": "done", "envelope": map[string]any{"schema": "comitia.discussion.v1", "task_id": task.ID, "payload": map[string]any{}}, "task_fence": map[string]any{"task_id": task.ID, "claim_token": renewed.Token, "lease_version": renewed.Task.LeaseVersion}}
			msg := call("send_message", result).(comitia.Message)
			if msg.TaskAttempt == nil || msg.TaskAttempt.TaskID != task.ID {
				t.Fatalf("fenced MCP result: %+v", msg)
			}
			if again := call("send_message", result).(comitia.Message); !reflect.DeepEqual(msg.TaskAttempt.TaskID, again.TaskAttempt.TaskID) || msg.ID != again.ID {
				t.Fatalf("fenced retry: %+v", again)
			}
			call("complete_task", map[string]any{"task_id": task.ID, "claim_token": renewed.Token, "lease_version": renewed.Task.LeaseVersion})
			result["dedupe_key"] = "stale"
			raw, _ := json.Marshal(result)
			if _, err := s.call(ctx, "send_message", raw); !errors.Is(err, comitia.ErrStaleClaim) {
				t.Fatalf("stale MCP result: %v", err)
			}
		})
	}
}
