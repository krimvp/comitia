package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/krimvp/comitia/internal/evaluation"
	"github.com/krimvp/comitia/internal/model"
)

func TestEvaluationEnvelopeRequiresTask(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	payload := json.RawMessage(`{"evaluation_id":"eval-1","outcome":"accepted","grader":{"kind":"automated","id":"ci"},"criteria":[{"name":"tests","outcome":"passed","evidence_refs":["ci:1"]}]}`)
	_, err := f.svc.AppendMessage(context.Background(), f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "evaluation", Envelope: &model.MessageEnvelope{Schema: model.EvaluationResultSchema, Payload: payload}})
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("missing task accepted: %v", err)
	}
	task, err := f.svc.CreateTask(context.Background(), f.admin.Token, f.context.ID, model.TaskInput{Title: "evaluate"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := f.svc.AppendMessage(context.Background(), f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "evaluation-valid", Envelope: &model.MessageEnvelope{Schema: model.EvaluationResultSchema, TaskID: task.ID, Payload: payload}})
	if err != nil {
		t.Fatalf("valid evaluation rejected: %v", err)
	}
	result, err := evaluation.ParseEvaluationResult(message)
	if err != nil || result.EvaluationID != "eval-1" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	got, err := f.svc.GetTask(context.Background(), f.admin.Token, task.ID)
	if err != nil || got.Status != model.TaskPending {
		t.Fatalf("evaluation changed task state: %+v %v", got, err)
	}
}
