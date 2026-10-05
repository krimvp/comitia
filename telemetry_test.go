package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

func TestExecutionEventValidationAndReplay(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "evaluate candidate"})
	if err != nil {
		t.Fatal(err)
	}
	payload := json.RawMessage(`{"run_id":"eval-17/candidate-a","event_sequence":2,"kind":"model_usage","occurred_at":"2026-09-11T08:30:00.123Z","provider":"example","model":"candidate-a","input_tokens":1200,"output_tokens":85,"cached_input_tokens":400,"duration_ms":712,"cost_micros":1450,"currency":"USD","cost_source":"estimated","status":"succeeded","dimensions":{"suite":"reasoning-v2","case":"case-004"}}`)
	message, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "execution/eval-17/candidate-a/2", Envelope: &MessageEnvelope{Schema: ExecutionEventSchema, TaskID: task.ID, Payload: payload}})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 10, MessageFilters{Schema: ExecutionEventSchema})
	if err != nil || len(messages) != 1 || !sameEnvelope(messages[0].Envelope, message.Envelope) {
		t.Fatalf("execution event replay: %v %v", messages, err)
	}

	tests := []struct{ name, payload, taskID string }{
		{"task required", `{"run_id":"r","event_sequence":1,"kind":"run_started"}`, ""},
		{"positive sequence", `{"run_id":"r","event_sequence":0,"kind":"run_started"}`, task.ID},
		{"known kind", `{"run_id":"r","event_sequence":1,"kind":"prompt"}`, task.ID},
		{"model required", `{"run_id":"r","event_sequence":1,"kind":"model_usage","input_tokens":1}`, task.ID},
		{"measurement required", `{"run_id":"r","event_sequence":1,"kind":"model_usage","model":"m"}`, task.ID},
		{"nonnegative usage", `{"run_id":"r","event_sequence":1,"kind":"model_usage","model":"m","input_tokens":-1}`, task.ID},
		{"tool required", `{"run_id":"r","event_sequence":1,"kind":"tool_usage"}`, task.ID},
		{"finish status required", `{"run_id":"r","event_sequence":1,"kind":"run_finished"}`, task.ID},
		{"known status", `{"run_id":"r","event_sequence":1,"kind":"run_finished","status":"ok"}`, task.ID},
		{"cost metadata required", `{"run_id":"r","event_sequence":1,"kind":"model_usage","model":"m","cost_micros":2}`, task.ID},
		{"currency uppercase", `{"run_id":"r","event_sequence":1,"kind":"model_usage","model":"m","cost_micros":2,"currency":"usd","cost_source":"estimated"}`, task.ID},
		{"known cost source", `{"run_id":"r","event_sequence":1,"kind":"model_usage","model":"m","cost_micros":2,"currency":"USD","cost_source":"guess"}`, task.ID},
		{"valid timestamp", `{"run_id":"r","event_sequence":1,"kind":"run_started","occurred_at":"yesterday"}`, task.ID},
		{"unknown field", `{"run_id":"r","event_sequence":1,"kind":"run_started","prompt":"secret"}`, task.ID},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: fmt.Sprintf("invalid-%d", i), Envelope: &MessageEnvelope{Schema: ExecutionEventSchema, TaskID: tc.taskID, Payload: json.RawMessage(tc.payload)}})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("got %v, want invalid input", err)
			}
		})
	}
}
