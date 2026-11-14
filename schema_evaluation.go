package comitia

import (
	"encoding/json"
	"strings"
)

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

func validEvaluationResult(raw json.RawMessage) error {
	var p EvaluationResultPayload
	if err := strictPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.EvaluationID) == "" {
		return Invalidf("evaluation_id required")
	}
	if strings.TrimSpace(p.Grader.ID) == "" {
		return Invalidf("grader.id required")
	}
	switch p.Grader.Kind {
	case GraderHuman, GraderAgent, GraderAutomated:
	default:
		return Invalidf("grader.kind must be human, agent or automated")
	}
	if len(p.Criteria) == 0 {
		return Invalidf("criteria required")
	}
	allPassed, hasFailed, hasNotRun := true, false, false
	names := make(map[string]struct{}, len(p.Criteria))
	for _, criterion := range p.Criteria {
		name := strings.TrimSpace(criterion.Name)
		if name == "" {
			return Invalidf("criteria.name required")
		}
		if _, duplicate := names[name]; duplicate {
			return Invalidf("criteria.name must be unique")
		}
		names[name] = struct{}{}
		switch criterion.Outcome {
		case CriterionPassed:
			if !nonempty(criterion.EvidenceRefs) {
				return Invalidf("criteria.evidence_refs required for passed outcome")
			}
		case CriterionFailed:
			allPassed = false
			hasFailed = true
			if !nonempty(criterion.EvidenceRefs) {
				return Invalidf("criteria.evidence_refs required for failed outcome")
			}
		case CriterionNotRun:
			allPassed = false
			hasNotRun = true
			if strings.TrimSpace(criterion.Reason) == "" {
				return Invalidf("criteria.reason required for not_run outcome")
			}
		default:
			return Invalidf("criteria.outcome must be passed, failed or not_run")
		}
	}
	switch p.Outcome {
	case EvaluationAccepted:
		if !allPassed {
			return Invalidf("outcome accepted requires all criteria passed")
		}
	case EvaluationRejected:
		if !hasFailed {
			return Invalidf("outcome rejected requires a failed criterion")
		}
	case EvaluationInconclusive:
		if hasFailed || !hasNotRun {
			return Invalidf("outcome inconclusive requires not_run criteria and no failed criteria")
		}
	default:
		return Invalidf("outcome must be accepted, rejected or inconclusive")
	}
	if p.Cost != nil && (p.Cost.AmountMicros < 0 || !currencyCode(p.Cost.Currency)) {
		return Invalidf("cost.amount_micros must be nonnegative and cost.currency an uppercase three-letter code")
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
