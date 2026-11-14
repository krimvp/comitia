package comitia

import (
	"context"
	"encoding/json"
)

const ApprovalSchema = "comitia.approval.v1"

const (
	RequestSchema          = "comitia.request.v1"
	ReceiptSchema          = "comitia.receipt.v1"
	MergeCertificateSchema = "comitia.merge-certificate.v1"
	MergeReceiptSchema     = "comitia.merge-receipt.v1"
)

const EvaluationResultSchema = "comitia.evaluation-result.v1"

// MessageSchemaDescriptor describes the registered payload contract. Requirements
// document semantic checks beyond JSON Schema; the payload validator is authoritative.
// Opaque marks legacy extensions whose validator has no supplied descriptor.
// ServerAuthored marks built-ins that replay can return but append rejects.
type MessageSchemaDescriptor struct {
	ID                 string                 `json:"id"`
	PayloadSchema      json.RawMessage        `json:"payload_schema"`
	BodyRequired       bool                   `json:"body_required"`
	TaskRequired       bool                   `json:"task_required"`
	TaskOrArtifactRefs bool                   `json:"task_or_artifact_refs"`
	Requirements       []string               `json:"requirements"`
	Examples           []MessageSchemaExample `json:"examples"`
	Opaque             bool                   `json:"opaque,omitempty"`
	ServerAuthored     bool                   `json:"server_authored,omitempty"`
}

type MessageSchemaExample struct {
	Body     string          `json:"body,omitempty"`
	Envelope MessageEnvelope `json:"envelope"`
}

// MessageSchemaAPI is optional for existing API implementations. Discovery needs
// a valid session, but no context grant: it contains contracts, never context data.
type MessageSchemaAPI interface {
	ListMessageSchemas(context.Context, string) ([]MessageSchemaDescriptor, error)
	GetMessageSchema(context.Context, string, string) (MessageSchemaDescriptor, error)
}

// StanceSchema is a typed agree/refine/disagree reply; envelope.reply_to is required.
const StanceSchema = "comitia.stance.v1"

// TaskStatusSchema reports a task's progress in the parent context, typically
// from a coordinator working in a private child context. Nothing is relayed
// between contexts; the sender posts the summary itself.
const TaskStatusSchema = "comitia.task-status.v1"

// TaskEventSchema is reserved for transitions committed by the service.
const TaskEventSchema = "comitia.task-event.v1"

// ExecutionEventSchema identifies runner-reported execution telemetry. Events
// use the surrounding message for context, task, authenticated session, durable
// ordering, and idempotent ingestion.
const ExecutionEventSchema = "comitia.execution-event.v1"
