package comitia

import (
	"context"
	"time"

	"github.com/krimvp/comitia/internal/evaluation"
	"github.com/krimvp/comitia/internal/mergegate"
	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/schema"
	"github.com/krimvp/comitia/internal/service"
)

// Messages, their schemas and payloads, and the merge gate and evaluation
// reports built on them.

// InstanceDefaults holds validated process defaults.
type InstanceDefaults = model.InstanceDefaults

func NewInstanceDefaults(instance *AgentInstance) (InstanceDefaults, error) {
	return model.NewInstanceDefaults(instance)
}

// InstanceDefaultsFromEnv captures the environment once at startup. Empty values
// disable defaults; a label without an ID and malformed values are errors.
func InstanceDefaultsFromEnv() (InstanceDefaults, error) {
	return model.InstanceDefaultsFromEnv()
}

type Message = model.Message

// AgentInstance is caller-claimed coordination metadata, never an authenticated
// identity.
type AgentInstance = model.AgentInstance

type MessageInput = model.MessageInput

type MessageEnvelope = model.MessageEnvelope

// MessageFilters narrow replay within the same context read permission.
type MessageFilters = model.MessageFilters

type PayloadValidator = model.PayloadValidator

type Participant = model.Participant

type MessageAPI = model.MessageAPI

const DefaultMessageBytes = model.DefaultMessageBytes

const MaxMessageBytes = model.MaxMessageBytes

// MessagePage preserves whole messages.
type MessagePage = model.MessagePage

// MessagePageAPI is optional so existing embedded implementations remain valid.
type MessagePageAPI = model.MessagePageAPI

// MessageQueryAPI opts into lookup and all scoped MessageFilters.
type MessageQueryAPI = model.MessageQueryAPI

// MessageReadOptionsAPI opts into self exclusion, cursor starts and schema
// lists.
type MessageReadOptionsAPI = model.MessageReadOptionsAPI

const // MaxDedupeKeyBytes bounds new dedupe keys; existing longer keys stay
// readable.
MaxDedupeKeyBytes = model.MaxDedupeKeyBytes

// DedupeConflictError reports a send whose dedupe scope already holds a
// different message.
type DedupeConflictError = model.DedupeConflictError

// TaskFence authenticates a result against the current live lease in the append
// transaction.
type TaskFence = model.TaskFence

// TaskAttempt is server-validated provenance.
type TaskAttempt = model.TaskAttempt

const MaxMessageWait = model.MaxMessageWait

type ParticipantPage = model.ParticipantPage

type ParticipantPageAPI = model.ParticipantPageAPI

func LookupMessage(ctx context.Context, api MessageAPI, token, cid, mid string) (Message, error) {
	return service.LookupMessage(ctx, api, token, cid, mid)
}

// CheckMessageQueryAPI prevents adapters from widening reads through old APIs.
func CheckMessageQueryAPI(api MessageAPI, f MessageFilters) error {
	return service.CheckMessageQueryAPI(api, f)
}

// MessageWaitTimeout converts wire seconds without overflowing a duration.
func MessageWaitTimeout(seconds float64) (time.Duration, error) {
	return service.MessageWaitTimeout(seconds)
}

func ReplayMessagePage(ctx context.Context, api MessageAPI, token, cid string, after int64, limit int, filters ...MessageFilters) (MessagePage, error) {
	return service.ReplayMessagePage(ctx, api, token, cid, after, limit, filters...)
}

func WaitMessagePage(ctx context.Context, api MessageAPI, token, cid string, after int64, timeout time.Duration, filters ...MessageFilters) (MessagePage, error) {
	return service.WaitMessagePage(ctx, api, token, cid, after, timeout, filters...)
}

func ListParticipantPage(ctx context.Context, api MessageAPI, token, cid, after string, limit int) (ParticipantPage, error) {
	return service.ListParticipantPage(ctx, api, token, cid, after, limit)
}

const ApprovalSchema = model.ApprovalSchema

const (
	RequestSchema          = model.RequestSchema
	ReceiptSchema          = model.ReceiptSchema
	MergeCertificateSchema = model.MergeCertificateSchema
	MergeReceiptSchema     = model.MergeReceiptSchema
)

const EvaluationResultSchema = model.EvaluationResultSchema

// MessageSchemaDescriptor describes the registered payload contract.
type MessageSchemaDescriptor = model.MessageSchemaDescriptor

type MessageSchemaExample = model.MessageSchemaExample

// MessageSchemaAPI is optional for existing API implementations.
type MessageSchemaAPI = model.MessageSchemaAPI

const // StanceSchema is a typed agree/refine/disagree reply; envelope.reply_to is
// required.
StanceSchema = model.StanceSchema

const // TaskStatusSchema reports a task's progress in the parent context, typically
// from a coordinator working in a private child context.
TaskStatusSchema = model.TaskStatusSchema

const // TaskEventSchema is reserved for transitions committed by the service.
TaskEventSchema = model.TaskEventSchema

const // ExecutionEventSchema identifies runner-reported execution telemetry.
ExecutionEventSchema = model.ExecutionEventSchema

type ApprovalVerdict = schema.ApprovalVerdict

const (
	ApprovalApproved         = schema.ApprovalApproved
	ApprovalChangesRequested = schema.ApprovalChangesRequested
)

// ApprovalPayload is a reviewer's verdict on exact artifacts.
type ApprovalPayload = schema.ApprovalPayload

// RequestPayload persists a business deadline, independent of a wait timeout.
type RequestPayload = schema.RequestPayload

type ReceiptPayload = schema.ReceiptPayload

type MergeCertificatePayload = schema.MergeCertificatePayload

type MergeReceiptPayload = schema.MergeReceiptPayload

type TaskEventPayload = schema.TaskEventPayload

type TaskStatusPayload = schema.TaskStatusPayload

type EvaluationOutcome = schema.EvaluationOutcome

const (
	EvaluationAccepted     = schema.EvaluationAccepted
	EvaluationRejected     = schema.EvaluationRejected
	EvaluationInconclusive = schema.EvaluationInconclusive
)

type CriterionOutcome = schema.CriterionOutcome

const (
	CriterionPassed = schema.CriterionPassed
	CriterionFailed = schema.CriterionFailed
	CriterionNotRun = schema.CriterionNotRun
)

type GraderKind = schema.GraderKind

const (
	GraderHuman     = schema.GraderHuman
	GraderAgent     = schema.GraderAgent
	GraderAutomated = schema.GraderAutomated
)

// EvaluationResultPayload is the machine-readable payload of an
// EvaluationResultSchema message.
type EvaluationResultPayload = schema.EvaluationResultPayload

type EvaluationGrader = schema.EvaluationGrader

type EvaluationCriterion = schema.EvaluationCriterion

// EvaluationCost records an optional monetary cost in millionths of a currency
// unit.
type EvaluationCost = schema.EvaluationCost

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

// MergePolicy is launcher/gate-owned configuration, never supplied by a
// reviewer.
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
