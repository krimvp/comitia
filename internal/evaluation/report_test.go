package evaluation

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/krimvp/comitia/internal/model"
)

func evaluationMessage(t *testing.T, taskID, payload string) model.Message {
	t.Helper()
	return model.Message{Envelope: &model.MessageEnvelope{Schema: model.EvaluationResultSchema, TaskID: taskID, Payload: json.RawMessage(payload)}}
}

func TestBuildEvaluationReportRequiresCompleteCostCoverage(t *testing.T) {
	messages := []model.Message{
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
	_, err := BuildEvaluationReport([]model.Message{evaluationMessage(t, "task-1", payload), evaluationMessage(t, "task-2", payload)})
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("duplicate accepted: %v", err)
	}
}

func TestBuildEvaluationReportRejectsCostOverflow(t *testing.T) {
	messages := []model.Message{
		evaluationMessage(t, "task-1", `{"evaluation_id":"eval-1","outcome":"accepted","grader":{"kind":"automated","id":"ci"},"criteria":[{"name":"tests","outcome":"passed","evidence_refs":["ci:1"]}],"cost":{"amount_micros":9223372036854775807,"currency":"USD"}}`),
		evaluationMessage(t, "task-2", `{"evaluation_id":"eval-2","outcome":"rejected","grader":{"kind":"automated","id":"ci"},"criteria":[{"name":"tests","outcome":"failed","evidence_refs":["ci:2"]}],"cost":{"amount_micros":1,"currency":"USD"}}`),
	}
	_, err := BuildEvaluationReport(messages)
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("overflow accepted: %v", err)
	}
}
