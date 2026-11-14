package comitia

import (
	"github.com/krimvp/comitia/internal/schema"
)

type ApprovalVerdict = schema.ApprovalVerdict

const ApprovalApproved = schema.ApprovalApproved

const ApprovalChangesRequested = schema.ApprovalChangesRequested

// ApprovalPayload is a reviewer's verdict on exact artifacts. Comitia checks its
type ApprovalPayload = schema.ApprovalPayload

// RequestPayload persists a business deadline, independent of a wait timeout.
type RequestPayload = schema.RequestPayload

type ReceiptPayload = schema.ReceiptPayload

type MergeCertificatePayload = schema.MergeCertificatePayload

type MergeReceiptPayload = schema.MergeReceiptPayload

type EvaluationOutcome = schema.EvaluationOutcome

const EvaluationAccepted = schema.EvaluationAccepted

const EvaluationRejected = schema.EvaluationRejected

const EvaluationInconclusive = schema.EvaluationInconclusive

type CriterionOutcome = schema.CriterionOutcome

const CriterionPassed = schema.CriterionPassed

const CriterionFailed = schema.CriterionFailed

const CriterionNotRun = schema.CriterionNotRun

type GraderKind = schema.GraderKind

const GraderHuman = schema.GraderHuman

const GraderAgent = schema.GraderAgent

const GraderAutomated = schema.GraderAutomated

// EvaluationResultPayload is the machine-readable payload of an
type EvaluationResultPayload = schema.EvaluationResultPayload

type EvaluationGrader = schema.EvaluationGrader

type EvaluationCriterion = schema.EvaluationCriterion

// EvaluationCost records an optional monetary cost in millionths of a currency
type EvaluationCost = schema.EvaluationCost

type TaskEventPayload = schema.TaskEventPayload

type TaskStatusPayload = schema.TaskStatusPayload
