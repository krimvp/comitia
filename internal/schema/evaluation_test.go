package schema

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/krimvp/comitia/internal/model"
)

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
			if err := ValidEvaluationResult(json.RawMessage(tc.payload)); err != nil {
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
		if err := ValidEvaluationResult(json.RawMessage(payload)); !errors.Is(err, model.ErrInvalid) {
			t.Errorf("invalid payload %d accepted: %v", i, err)
		}
	}
}
