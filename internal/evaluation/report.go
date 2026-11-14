package evaluation

import (
	"fmt"
	"math"
	"sort"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/schema"
)

type EvaluationOutcomeCounts struct {
	Accepted     int `json:"accepted"`
	Rejected     int `json:"rejected"`
	Inconclusive int `json:"inconclusive"`
}

type EvaluationCostMetric struct {
	Currency                    string   `json:"currency"`
	TotalAmountMicros           int64    `json:"total_amount_micros"`
	CostPerAcceptedResultMicros *float64 `json:"cost_per_accepted_result_micros,omitempty"`
}

// EvaluationReport is a deterministic aggregate of evaluation-result messages.
// CostPerAcceptedResultMicros is omitted unless every result has a cost and at
// least one result was accepted, preventing incomplete cost data from looking
// like a complete efficiency metric.
type EvaluationReport struct {
	Schema                 string                  `json:"schema"`
	TotalResults           int                     `json:"total_results"`
	Outcomes               EvaluationOutcomeCounts `json:"outcomes"`
	AcceptanceRate         *float64                `json:"acceptance_rate,omitempty"`
	TotalCriteria          int                     `json:"total_criteria"`
	EvidenceBackedCriteria int                     `json:"evidence_backed_criteria"`
	NotRunCriteria         int                     `json:"not_run_criteria"`
	CostedResults          int                     `json:"costed_results"`
	UncostedResults        int                     `json:"uncosted_results"`
	Costs                  []EvaluationCostMetric  `json:"costs"`
}

// ParseEvaluationResult extracts and validates one evaluation-result message.
func ParseEvaluationResult(message model.Message) (schema.EvaluationResultPayload, error) {
	var result schema.EvaluationResultPayload
	if message.Envelope == nil || message.Envelope.Schema != model.EvaluationResultSchema || message.Envelope.TaskID == "" {
		return result, fmt.Errorf("%w: evaluation-result envelope required", model.ErrInvalid)
	}
	if err := schema.ValidEvaluationResult(message.Envelope.Payload); err != nil {
		return schema.EvaluationResultPayload{}, err
	}
	if err := schema.StrictPayload(message.Envelope.Payload, &result); err != nil {
		return schema.EvaluationResultPayload{}, err
	}
	return result, nil
}

// BuildEvaluationReport aggregates evaluation-result messages and ignores other
// message schemas. Evaluation IDs must be unique within the supplied window.
func BuildEvaluationReport(messages []model.Message) (EvaluationReport, error) {
	report := EvaluationReport{Schema: "comitia.evaluation-report.v1", Costs: []EvaluationCostMetric{}}
	totals := map[string]int64{}
	seen := map[string]struct{}{}
	for _, message := range messages {
		if message.Envelope == nil || message.Envelope.Schema != model.EvaluationResultSchema {
			continue
		}
		result, err := ParseEvaluationResult(message)
		if err != nil {
			return EvaluationReport{}, err
		}
		if _, duplicate := seen[result.EvaluationID]; duplicate {
			return EvaluationReport{}, model.Invalidf("evaluation_id must be unique")
		}
		seen[result.EvaluationID] = struct{}{}
		report.TotalResults++
		switch result.Outcome {
		case schema.EvaluationAccepted:
			report.Outcomes.Accepted++
		case schema.EvaluationRejected:
			report.Outcomes.Rejected++
		case schema.EvaluationInconclusive:
			report.Outcomes.Inconclusive++
		}
		for _, criterion := range result.Criteria {
			report.TotalCriteria++
			if criterion.Outcome == schema.CriterionNotRun {
				report.NotRunCriteria++
			} else {
				report.EvidenceBackedCriteria++
			}
		}
		if result.Cost == nil {
			report.UncostedResults++
		} else {
			report.CostedResults++
			if result.Cost.AmountMicros > math.MaxInt64-totals[result.Cost.Currency] {
				return EvaluationReport{}, fmt.Errorf("%w: cost total overflow for %s", model.ErrInvalid, result.Cost.Currency)
			}
			totals[result.Cost.Currency] += result.Cost.AmountMicros
		}
	}
	if report.TotalResults > 0 {
		rate := float64(report.Outcomes.Accepted) / float64(report.TotalResults)
		report.AcceptanceRate = &rate
	}
	currencies := make([]string, 0, len(totals))
	for currency := range totals {
		currencies = append(currencies, currency)
	}
	sort.Strings(currencies)
	for _, currency := range currencies {
		metric := EvaluationCostMetric{Currency: currency, TotalAmountMicros: totals[currency]}
		if report.UncostedResults == 0 && report.Outcomes.Accepted > 0 {
			perAccepted := float64(metric.TotalAmountMicros) / float64(report.Outcomes.Accepted)
			metric.CostPerAcceptedResultMicros = &perAccepted
		}
		report.Costs = append(report.Costs, metric)
	}
	return report, nil
}
