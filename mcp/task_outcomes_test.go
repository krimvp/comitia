package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/krimvp/comitia"
)

type outcomeAPI struct {
	comitia.API
	input  comitia.TaskOutcomeInput
	status comitia.TaskStatus
}

func (a *outcomeAPI) FailTask(_ context.Context, _, tid string, in comitia.TaskOutcomeInput) (comitia.Task, error) {
	a.input, a.status = in, comitia.TaskFailed
	return comitia.Task{ID: tid, Status: a.status, OutcomeReason: in.Reason}, nil
}
func (a *outcomeAPI) CancelTask(_ context.Context, _, tid string, in comitia.TaskOutcomeInput) (comitia.Task, error) {
	a.input, a.status = in, comitia.TaskCancelled
	return comitia.Task{ID: tid, Status: a.status, OutcomeReason: in.Reason}, nil
}

func TestTaskOutcomeTools(t *testing.T) {
	a := &outcomeAPI{}
	s := &Server{API: a, Credential: "secret"}
	ctx := context.Background()
	for _, name := range []string{"fail_task", "cancel_task"} {
		v, err := s.call(ctx, name, json.RawMessage(`{"task_id":"t","claim_token":"k","lease_version":2,"reason":"stopped","operation_id":"finish"}`))
		if err != nil || v.(comitia.Task).OutcomeReason != "stopped" || a.input != (comitia.TaskOutcomeInput{ClaimToken: "k", LeaseVersion: 2, Reason: "stopped", OperationID: "finish"}) {
			t.Fatalf("%s: %+v %v", name, v, err)
		}
		for _, raw := range []string{`{"task_id":"t","reason":"r"}`, `{"task_id":"t","lease_version":1}`, `{"lease_version":1,"reason":"r"}`, `{"task_id":"t","lease_version":1,"reason":"r","extra":1}`} {
			if _, err := s.call(ctx, name, json.RawMessage(raw)); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("%s %s: %v", name, raw, err)
			}
		}
	}
	if _, err := s.call(ctx, "cancel_task", json.RawMessage(`{"task_id":"t","lease_version":0,"reason":"cancel pending"}`)); err != nil || a.input.LeaseVersion != 0 || a.input.ClaimToken != "" {
		t.Fatalf("admin cancel: %+v %v", a.input, err)
	}
	s.API = &claimAPI{}
	if _, err := s.call(ctx, "cancel_task", json.RawMessage(`{"task_id":"t","lease_version":0,"reason":"r"}`)); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("legacy API: %v", err)
	}
	for _, status := range []string{"failed", "cancelled"} {
		s.API = &taskListOutcomeAPI{}
		if _, err := s.call(ctx, "list_tasks", json.RawMessage(`{"context_id":"c","status":"`+status+`"}`)); err != nil {
			t.Fatalf("outcome filter: %v", err)
		}
	}
}

type taskListOutcomeAPI struct{ comitia.API }

func (*taskListOutcomeAPI) ListTasks(_ context.Context, _, _ string, status comitia.TaskStatus, _ int) ([]comitia.Task, error) {
	return []comitia.Task{{Status: status}}, nil
}
