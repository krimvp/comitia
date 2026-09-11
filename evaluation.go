package comitia

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
)

const EvaluationResultSchema = "comitia.evaluation-result.v1"

type EvaluationOutcome string

const (
	EvaluationAccepted     EvaluationOutcome = "accepted"
	EvaluationRejected     EvaluationOutcome = "rejected"
	EvaluationInconclusive EvaluationOutcome = "inconclusive"
)

type CriterionOutcome string

const (
	CriterionPassed CriterionOutcome = "passed"
	CriterionFailed CriterionOutcome = "failed"
	CriterionNotRun CriterionOutcome = "not_run"
)

type GraderKind string

const (
	GraderHuman     GraderKind = "human"
	GraderAgent     GraderKind = "agent"
	GraderAutomated GraderKind = "automated"
)

// EvaluationResultPayload is the machine-readable payload of an
// EvaluationResultSchema message. Evidence references are opaque identifiers;
// Comitia validates their presence but does not fetch or endorse them.
type EvaluationResultPayload struct {
	EvaluationID string                `json:"evaluation_id"`
	Outcome      EvaluationOutcome     `json:"outcome"`
	Grader       EvaluationGrader      `json:"grader"`
	Criteria     []EvaluationCriterion `json:"criteria"`
	Cost         *EvaluationCost       `json:"cost,omitempty"`
}

type EvaluationGrader struct {
	Kind GraderKind `json:"kind"`
	ID   string     `json:"id"`
}

type EvaluationCriterion struct {
	Name         string           `json:"name"`
	Outcome      CriterionOutcome `json:"outcome"`
	EvidenceRefs []string         `json:"evidence_refs,omitempty"`
	Reason       string           `json:"reason,omitempty"`
}

// EvaluationCost records an optional monetary cost in millionths of a currency
// unit. Currency is a three-letter uppercase code, such as USD or EUR.
type EvaluationCost struct {
	AmountMicros int64  `json:"amount_micros"`
	Currency     string `json:"currency"`
}

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

func validEvaluationResult(raw json.RawMessage) error {
	var p EvaluationResultPayload
	if strictPayload(raw, &p) != nil || strings.TrimSpace(p.EvaluationID) == "" {
		return ErrInvalid
	}
	if strings.TrimSpace(p.Grader.ID) == "" {
		return ErrInvalid
	}
	switch p.Grader.Kind {
	case GraderHuman, GraderAgent, GraderAutomated:
	default:
		return ErrInvalid
	}
	if len(p.Criteria) == 0 {
		return ErrInvalid
	}
	allPassed, hasFailed, hasNotRun := true, false, false
	names := make(map[string]struct{}, len(p.Criteria))
	for _, criterion := range p.Criteria {
		name := strings.TrimSpace(criterion.Name)
		if name == "" {
			return ErrInvalid
		}
		if _, duplicate := names[name]; duplicate {
			return ErrInvalid
		}
		names[name] = struct{}{}
		switch criterion.Outcome {
		case CriterionPassed:
			if !nonempty(criterion.EvidenceRefs) {
				return ErrInvalid
			}
		case CriterionFailed:
			allPassed = false
			hasFailed = true
			if !nonempty(criterion.EvidenceRefs) {
				return ErrInvalid
			}
		case CriterionNotRun:
			allPassed = false
			hasNotRun = true
			if strings.TrimSpace(criterion.Reason) == "" {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	switch p.Outcome {
	case EvaluationAccepted:
		if !allPassed {
			return ErrInvalid
		}
	case EvaluationRejected:
		if !hasFailed {
			return ErrInvalid
		}
	case EvaluationInconclusive:
		if hasFailed || !hasNotRun {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if p.Cost != nil && (p.Cost.AmountMicros < 0 || !currencyCode(p.Cost.Currency)) {
		return ErrInvalid
	}
	return nil
}

func currencyCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, r := range s {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return true
}

// ParseEvaluationResult extracts and validates one evaluation-result message.
func ParseEvaluationResult(message Message) (EvaluationResultPayload, error) {
	var result EvaluationResultPayload
	if message.Envelope == nil || message.Envelope.Schema != EvaluationResultSchema || message.Envelope.TaskID == "" {
		return result, fmt.Errorf("%w: evaluation-result envelope required", ErrInvalid)
	}
	if validEvaluationResult(message.Envelope.Payload) != nil || strictPayload(message.Envelope.Payload, &result) != nil {
		return EvaluationResultPayload{}, fmt.Errorf("%w: invalid evaluation result", ErrInvalid)
	}
	return result, nil
}

// BuildEvaluationReport aggregates evaluation-result messages and ignores other
// message schemas. Evaluation IDs must be unique within the supplied window.
func BuildEvaluationReport(messages []Message) (EvaluationReport, error) {
	report := EvaluationReport{Schema: "comitia.evaluation-report.v1", Costs: []EvaluationCostMetric{}}
	totals := map[string]int64{}
	seen := map[string]struct{}{}
	for _, message := range messages {
		if message.Envelope == nil || message.Envelope.Schema != EvaluationResultSchema {
			continue
		}
		result, err := ParseEvaluationResult(message)
		if err != nil {
			return EvaluationReport{}, err
		}
		if _, duplicate := seen[result.EvaluationID]; duplicate {
			return EvaluationReport{}, fmt.Errorf("%w: duplicate evaluation_id %q", ErrInvalid, result.EvaluationID)
		}
		seen[result.EvaluationID] = struct{}{}
		report.TotalResults++
		switch result.Outcome {
		case EvaluationAccepted:
			report.Outcomes.Accepted++
		case EvaluationRejected:
			report.Outcomes.Rejected++
		case EvaluationInconclusive:
			report.Outcomes.Inconclusive++
		}
		for _, criterion := range result.Criteria {
			report.TotalCriteria++
			if criterion.Outcome == CriterionNotRun {
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
				return EvaluationReport{}, fmt.Errorf("%w: cost total overflow for %s", ErrInvalid, result.Cost.Currency)
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
