package comitia

import (
	"time"

	"github.com/krimvp/comitia/internal/evaluation"
	"github.com/krimvp/comitia/internal/mergegate"
)

type EvaluationOutcomeCounts = evaluation.EvaluationOutcomeCounts

type EvaluationCostMetric = evaluation.EvaluationCostMetric

// EvaluationReport is a deterministic aggregate of evaluation-result messages.
type EvaluationReport = evaluation.EvaluationReport

// ParseEvaluationResult extracts and validates one evaluation-result message.
func ParseEvaluationResult(message Message) (EvaluationResultPayload, error) {
	return evaluation.ParseEvaluationResult(message)
}

// BuildEvaluationReport aggregates evaluation-result messages and ignores other
// message schemas. Evaluation IDs must be unique within the supplied window.
func BuildEvaluationReport(messages []Message) (EvaluationReport, error) {
	return evaluation.BuildEvaluationReport(messages)
}

// MergePolicy is launcher/gate-owned configuration, never supplied by a reviewer.
type MergePolicy = mergegate.MergePolicy

// MergeVerifyOptions are opt-in gate rules; the zero value adds none.
type MergeVerifyOptions = mergegate.MergeVerifyOptions

// VerifyMergeCertificate checks a snapshot, not GitHub or one-shot consumption.
// Inputs must come from authenticated Comitia reads, not arbitrary JSON files.
func VerifyMergeCertificate(cert, request Message, approvals []Message, policy MergePolicy, currentArtifact string, now time.Time) error {
	return mergegate.VerifyMergeCertificate(cert, request, approvals, policy, currentArtifact, now)
}

// VerifyMergeCertificateWithOptions is VerifyMergeCertificate plus opts.
func VerifyMergeCertificateWithOptions(cert, request Message, approvals []Message, policy MergePolicy, currentArtifact string, now time.Time, opts MergeVerifyOptions) error {
	return mergegate.VerifyMergeCertificateWithOptions(cert, request, approvals, policy, currentArtifact, now, opts)
}

// AuthoritativeMergeReceipt applies a fixed rule: only receipts sent by the
// gate's assigned mergerSessionID that match the certificate's reply, task,
// head and consumption key count; others are ignored. It returns the earliest
// one, ErrNotFound if none, or ErrConflict if they disagree on status/merge_sha.
func AuthoritativeMergeReceipt(cert Message, receipts []Message, mergerSessionID string) (Message, error) {
	return mergegate.AuthoritativeMergeReceipt(cert, receipts, mergerSessionID)
}
