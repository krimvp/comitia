package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/krimvp/comitia/internal/schema"
)

func evaluationMessage(t *testing.T, taskID, payload string) Message {
	t.Helper()
	return Message{Envelope: &MessageEnvelope{Schema: EvaluationResultSchema, TaskID: taskID, Payload: json.RawMessage(payload)}}
}

func TestEvaluationResultValidation(t *testing.T) {
	valid := `{"evaluation_id":"eval-1","outcome":"accepted","grader":{"kind":"automated","id":"ci/main"},"criteria":[{"name":"tests","outcome":"passed","evidence_refs":["ci:run/123"]}],"cost":{"amount_micros":125000,"currency":"USD"}}`
	tests := []struct {
		name    string
		payload string
	}{
		{"accepted", valid},
		{"rejected", `{"evaluation_id":"eval-2","outcome":"rejected","grader":{"kind":"human","id":"reviewer@example"},"criteria":[{"name":"review","outcome":"failed","evidence_refs":["finding:7"]}]}`},
		{"inconclusive", `{"evaluation_id":"eval-3","outcome":"inconclusive","grader":{"kind":"agent","id":"session:abc"},"criteria":[{"name":"integration","outcome":"not_run","reason":"dependency unavailable"}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := schema.ValidEvaluationResult(json.RawMessage(tc.payload)); err != nil {
				t.Fatalf("valid result rejected: %v", err)
			}
		})
	}
	invalid := []string{
		`{"evaluation_id":"eval","outcome":"accepted","grader":{"kind":"automated","id":"ci"},"criteria":[{"name":"tests","outcome":"passed"}]}`,
		`{"evaluation_id":"eval","outcome":"accepted","grader":{"kind":"automated","id":"ci"},"criteria":[{"name":"tests","outcome":"failed","evidence_refs":["log"]}]}`,
		`{"evaluation_id":"eval","outcome":"rejected","grader":{"kind":"human","id":"reviewer"},"criteria":[{"name":"tests","outcome":"passed","evidence_refs":["log"]}]}`,
		`{"evaluation_id":"eval","outcome":"inconclusive","grader":{"kind":"agent","id":"agent"},"criteria":[{"name":"tests","outcome":"failed","evidence_refs":["log"]}]}`,
		`{"evaluation_id":"eval","outcome":"inconclusive","grader":{"kind":"agent","id":"agent"},"criteria":[{"name":"tests","outcome":"not_run"}]}`,
		`{"evaluation_id":"eval","outcome":"accepted","grader":{"kind":"script","id":"ci"},"criteria":[{"name":"tests","outcome":"passed","evidence_refs":["log"]}]}`,
		`{"evaluation_id":"eval","outcome":"accepted","grader":{"kind":"automated","id":"ci"},"criteria":[{"name":"tests","outcome":"passed","evidence_refs":["log"]},{"name":"tests","outcome":"passed","evidence_refs":["other"]}]}`,
		`{"evaluation_id":"eval","outcome":"accepted","grader":{"kind":"automated","id":"ci"},"criteria":[{"name":"tests","outcome":"passed","evidence_refs":["log"]}],"cost":{"amount_micros":1,"currency":"usd"}}`,
		`{"evaluation_id":"eval","outcome":"accepted","grader":{"kind":"automated","id":"ci"},"criteria":[{"name":"tests","outcome":"passed","evidence_refs":["log"]}],"extra":true}`,
	}
	for i, payload := range invalid {
		if err := schema.ValidEvaluationResult(json.RawMessage(payload)); !errors.Is(err, ErrInvalid) {
			t.Errorf("invalid payload %d accepted: %v", i, err)
		}
	}
}

func TestEvaluationEnvelopeRequiresTask(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	payload := json.RawMessage(`{"evaluation_id":"eval-1","outcome":"accepted","grader":{"kind":"automated","id":"ci"},"criteria":[{"name":"tests","outcome":"passed","evidence_refs":["ci:1"]}]}`)
	_, err := f.svc.AppendMessage(context.Background(), f.admin.Token, f.context.ID, MessageInput{DedupeKey: "evaluation", Envelope: &MessageEnvelope{Schema: EvaluationResultSchema, Payload: payload}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing task accepted: %v", err)
	}
	task, err := f.svc.CreateTask(context.Background(), f.admin.Token, f.context.ID, TaskInput{Title: "evaluate"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := f.svc.AppendMessage(context.Background(), f.admin.Token, f.context.ID, MessageInput{DedupeKey: "evaluation-valid", Envelope: &MessageEnvelope{Schema: EvaluationResultSchema, TaskID: task.ID, Payload: payload}})
	if err != nil {
		t.Fatalf("valid evaluation rejected: %v", err)
	}
	result, err := ParseEvaluationResult(message)
	if err != nil || result.EvaluationID != "eval-1" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	got, err := f.svc.GetTask(context.Background(), f.admin.Token, task.ID)
	if err != nil || got.Status != TaskPending {
		t.Fatalf("evaluation changed task state: %+v %v", got, err)
	}
}

func TestBuildEvaluationReportRequiresCompleteCostCoverage(t *testing.T) {
	messages := []Message{
		evaluationMessage(t, "task-1", `{"evaluation_id":"eval-1","outcome":"accepted","grader":{"kind":"automated","id":"ci"},"criteria":[{"name":"tests","outcome":"passed","evidence_refs":["ci:1"]}],"cost":{"amount_micros":300000,"currency":"USD"}}`),
		{Body: "unrelated"},
		evaluationMessage(t, "task-2", `{"evaluation_id":"eval-2","outcome":"rejected","grader":{"kind":"human","id":"reviewer"},"criteria":[{"name":"quality","outcome":"failed","evidence_refs":["finding:1"]}],"cost":{"amount_micros":100000,"currency":"USD"}}`),
		evaluationMessage(t, "task-3", `{"evaluation_id":"eval-3","outcome":"inconclusive","grader":{"kind":"agent","id":"agent"},"criteria":[{"name":"deploy","outcome":"not_run","reason":"no environment"}],"cost":{"amount_micros":50000,"currency":"EUR"}}`),
	}
	report, err := BuildEvaluationReport(messages)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalResults != 3 || report.Outcomes.Accepted != 1 || report.Outcomes.Rejected != 1 || report.Outcomes.Inconclusive != 1 {
		t.Fatalf("wrong outcomes: %+v", report)
	}
	if report.AcceptanceRate == nil || *report.AcceptanceRate != 1.0/3.0 {
		t.Fatalf("wrong acceptance rate: %v", report.AcceptanceRate)
	}
	if report.TotalCriteria != 3 || report.EvidenceBackedCriteria != 2 || report.NotRunCriteria != 1 {
		t.Fatalf("wrong criteria: %+v", report)
	}
	if len(report.Costs) != 2 || report.Costs[0].Currency != "EUR" || report.Costs[1].Currency != "USD" {
		t.Fatalf("costs not deterministically ordered: %+v", report.Costs)
	}
	if got := report.Costs[1].CostPerAcceptedResultMicros; got == nil || *got != 400000 {
		t.Fatalf("wrong cost per accepted result: %v", got)
	}

	messages = append(messages, evaluationMessage(t, "task-4", `{"evaluation_id":"eval-4","outcome":"accepted","grader":{"kind":"human","id":"reviewer"},"criteria":[{"name":"review","outcome":"passed","evidence_refs":["review:2"]}]}`))
	report, err = BuildEvaluationReport(messages)
	if err != nil {
		t.Fatal(err)
	}
	if report.UncostedResults != 1 {
		t.Fatalf("uncosted=%d", report.UncostedResults)
	}
	for _, cost := range report.Costs {
		if cost.CostPerAcceptedResultMicros != nil {
			t.Fatalf("partial cost data produced efficiency metric: %+v", cost)
		}
	}
}

func TestBuildEvaluationReportRejectsDuplicateEvaluationID(t *testing.T) {
	payload := `{"evaluation_id":"same","outcome":"accepted","grader":{"kind":"automated","id":"ci"},"criteria":[{"name":"tests","outcome":"passed","evidence_refs":["ci:1"]}]}`
	_, err := BuildEvaluationReport([]Message{evaluationMessage(t, "task-1", payload), evaluationMessage(t, "task-2", payload)})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate accepted: %v", err)
	}
}

func TestBuildEvaluationReportRejectsCostOverflow(t *testing.T) {
	messages := []Message{
		evaluationMessage(t, "task-1", `{"evaluation_id":"eval-1","outcome":"accepted","grader":{"kind":"automated","id":"ci"},"criteria":[{"name":"tests","outcome":"passed","evidence_refs":["ci:1"]}],"cost":{"amount_micros":9223372036854775807,"currency":"USD"}}`),
		evaluationMessage(t, "task-2", `{"evaluation_id":"eval-2","outcome":"rejected","grader":{"kind":"automated","id":"ci"},"criteria":[{"name":"tests","outcome":"failed","evidence_refs":["ci:2"]}],"cost":{"amount_micros":1,"currency":"USD"}}`),
	}
	_, err := BuildEvaluationReport(messages)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("overflow accepted: %v", err)
	}
}
