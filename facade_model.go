package comitia

import (
	"encoding/json"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

// The build stamp is set on these root variables with -ldflags -X and copied
// into the implementation before any caller runs.
func init() {
	model.Version = Version
	model.Revision = Revision
	model.Dirty = Dirty
}

var ErrNotFound = model.ErrNotFound

var ErrUnauthenticated = model.ErrUnauthenticated

var ErrForbidden = model.ErrForbidden

var ErrConflict = model.ErrConflict

var ErrInvalid = model.ErrInvalid

var ErrStaleClaim = model.ErrStaleClaim

var ErrBootstrapUsed = model.ErrBootstrapUsed

type Permission = model.Permission

const PermissionRead = model.PermissionRead

const PermissionWrite = model.PermissionWrite

const PermissionClaim = model.PermissionClaim

const PermissionGrant = model.PermissionGrant

const PermissionAdmin = model.PermissionAdmin

// PermissionCreate allows creating child contexts under the context.
const PermissionCreate = model.PermissionCreate

const PermissionAll = model.PermissionAll

// API is the library-first application surface. Service and remote HTTP clients implement it.
type API = model.API

type Clock = model.Clock

type Config = model.Config

// Store supplies atomic persistence. Each View observes one consistent snapshot;
type Store = model.Store

type ReadStore = model.ReadStore

type WriteStore = model.WriteStore

// BatchContextReadStore is optional. Results must match individual Grants and
type BatchContextReadStore = model.BatchContextReadStore

type Workspace = model.Workspace

type WorkContext = model.WorkContext

type Reference = model.Reference

type CreateContextInput = model.CreateContextInput

type CreateChildContextInput = model.CreateChildContextInput

type AddReferenceInput = model.AddReferenceInput

type ContextAPI = model.ContextAPI

type ContextReadStore = model.ContextReadStore

type ContextWriteStore = model.ContextWriteStore

// ReferencePage lists a context's references by creation time and ID. Cursors
type ReferencePage = model.ReferencePage

// Reference listing is optional: implementations of the original API/store
type ReferenceListAPI = model.ReferenceListAPI

type ReferenceListStore = model.ReferenceListStore

// GrantLookup supplies raw records, including expired and revoked ones.
type GrantLookup = model.GrantLookup

// GrantEvaluator shares ancestry facts within one transaction. Discard it after
type GrantEvaluator = model.GrantEvaluator

func NewGrantEvaluator(lookup GrantLookup) *GrantEvaluator { return model.NewGrantEvaluator(lookup) }

// InstanceDefaults holds validated process defaults. Its zero value preserves
type InstanceDefaults = model.InstanceDefaults

func NewInstanceDefaults(instance *AgentInstance) (InstanceDefaults, error) {
	return model.NewInstanceDefaults(instance)
}

// InstanceDefaultsFromEnv captures the environment once at startup. Empty values
// disable defaults; a label without an ID and malformed values are errors.
func InstanceDefaultsFromEnv() (InstanceDefaults, error) { return model.InstanceDefaultsFromEnv() }

type Message = model.Message

// AgentInstance is caller-claimed coordination metadata, never an authenticated identity.
type AgentInstance = model.AgentInstance

type MessageInput = model.MessageInput

type MessageEnvelope = model.MessageEnvelope

// MessageFilters narrow replay within the same context read permission.
type MessageFilters = model.MessageFilters

type PayloadValidator = model.PayloadValidator

type Participant = model.Participant

type MessageAPI = model.MessageAPI

type MessageReadStore = model.MessageReadStore

type MessageWriteStore = model.MessageWriteStore

const DefaultMessageBytes = model.DefaultMessageBytes

const MaxMessageBytes = model.MaxMessageBytes

// MessagePage preserves whole messages. One message may exceed the soft byte
type MessagePage = model.MessagePage

// MessagePageAPI is optional so existing embedded implementations remain valid.
type MessagePageAPI = model.MessagePageAPI

// MessageQueryAPI opts into lookup and all scoped MessageFilters.
type MessageQueryAPI = model.MessageQueryAPI

// MessageQueryStore opts into the ReplyTo, ThreadID and FromSession filters.
type MessageQueryStore = model.MessageQueryStore

// MessageReadOptionsAPI opts into self exclusion, cursor starts and schema lists.
type MessageReadOptionsAPI = model.MessageReadOptionsAPI

// MessageReadOptionsStore opts into the ExcludeSelf and Schemas predicates.
type MessageReadOptionsStore = model.MessageReadOptionsStore

// MaxDedupeKeyBytes bounds new dedupe keys; existing longer keys stay readable.
const MaxDedupeKeyBytes = model.MaxDedupeKeyBytes

// DedupeConflictError reports a send whose dedupe scope already holds a different
type DedupeConflictError = model.DedupeConflictError

// TaskFence authenticates a result against the current live lease in the append transaction.
type TaskFence = model.TaskFence

// TaskAttempt is server-validated provenance. SessionID and Envelope.ReplyTo on
type TaskAttempt = model.TaskAttempt

const MaxMessageWait = model.MaxMessageWait

// MessageScanStore is an optional ReadStore capability for filtered reads.
type MessageScanStore = model.MessageScanStore

// ErrorInfo contains only public, safe recovery information.
type ErrorInfo = model.ErrorInfo

// ErrorDetails identifies the caller's own existing resource behind a conflict.
type ErrorDetails = model.ErrorDetails

// OperationalError preserves errors.Is classification across remote adapters.
type OperationalError = model.OperationalError

func ErrorClass(err error) string { return model.ErrorClass(err) }

func NewRequestID() string { return model.NewRequestID() }

func SafeError(operation string, err error, requestID string) ErrorInfo {
	return model.SafeError(operation, err, requestID)
}

// Diagnostic deliberately excludes errors, credentials, request bodies and client IDs.
type Diagnostic = model.Diagnostic

type DiagnosticHook = model.DiagnosticHook

func LogDiagnostic(d Diagnostic) { model.LogDiagnostic(d) }

func ReportFailure(hook DiagnosticHook, operation string, err error, requestID string, duration time.Duration) {
	model.ReportFailure(hook, operation, err, requestID, duration)
}

// FailureClass recognizes safe SQLite primary codes without inspecting error text.
func FailureClass(err error) string { return model.FailureClass(err) }

// SafeOperation maps untrusted route/tool names to fixed operational labels.
func SafeOperation(name string) string { return model.SafeOperation(name) }

// Discovery cursors are exclusive and never acknowledge messages. Task cursors
type TaskPage = model.TaskPage

type ParticipantPage = model.ParticipantPage

// Paging is optional: implementations of the original API/store contracts
type TaskPageAPI = model.TaskPageAPI

type ParticipantPageAPI = model.ParticipantPageAPI

type TaskPageStore = model.TaskPageStore

type ParticipantPageStore = model.ParticipantPageStore

const ListPageBytes = model.ListPageBytes

const MaxListBytes = model.MaxListBytes

const MaxInstanceResumeBytes = model.MaxInstanceResumeBytes

// Stores keep at most this many instances per (session, context); a new
const MaxInstancesPerSession = model.MaxInstancesPerSession

// Rows of revoked or expired sessions stay listed (inactive) for this long,
const InstanceRetention = model.InstanceRetention

// Unpaged ListInstances returns at most this many rows.
const DefaultInstanceListLimit = model.DefaultInstanceListLimit

const InstanceHeartbeatInterval = model.InstanceHeartbeatInterval

const InstanceRecentWindow = model.InstanceRecentWindow

// InstancePresence is self-reported process metadata, never proof of identity or liveness.
type InstancePresence = model.InstancePresence

// InstancePage cursors are exclusive "session_id/instance_id" pairs.
type InstancePage = model.InstancePage

// Paging is optional, like participant paging.
type InstancePageAPI = model.InstancePageAPI

type InstancePageStore = model.InstancePageStore

type PresenceAPI = model.PresenceAPI

type PresenceReadStore = model.PresenceReadStore

type PresenceWriteStore = model.PresenceWriteStore

const ApprovalSchema = model.ApprovalSchema

const RequestSchema = model.RequestSchema

const ReceiptSchema = model.ReceiptSchema

const MergeCertificateSchema = model.MergeCertificateSchema

const MergeReceiptSchema = model.MergeReceiptSchema

const EvaluationResultSchema = model.EvaluationResultSchema

// MessageSchemaDescriptor describes the registered payload contract. Requirements
type MessageSchemaDescriptor = model.MessageSchemaDescriptor

type MessageSchemaExample = model.MessageSchemaExample

// MessageSchemaAPI is optional for existing API implementations. Discovery needs
type MessageSchemaAPI = model.MessageSchemaAPI

// StanceSchema is a typed agree/refine/disagree reply; envelope.reply_to is required.
const StanceSchema = model.StanceSchema

// TaskStatusSchema reports a task's progress in the parent context, typically
const TaskStatusSchema = model.TaskStatusSchema

// TaskEventSchema is reserved for transitions committed by the service.
const TaskEventSchema = model.TaskEventSchema

// ExecutionEventSchema identifies runner-reported execution telemetry. Events
const ExecutionEventSchema = model.ExecutionEventSchema

type Grant = model.Grant

type GrantInput = model.GrantInput

type GrantAPI = model.GrantAPI

type GrantReadStore = model.GrantReadStore

type GrantWriteStore = model.GrantWriteStore

type Principal = model.Principal

type Session = model.Session

type Credential = model.Credential

type BootstrapInput = model.BootstrapInput

type DelegatedGrant = model.DelegatedGrant

type DelegateInput = model.DelegateInput

type SessionAPI = model.SessionAPI

type SessionReadStore = model.SessionReadStore

type SessionWriteStore = model.SessionWriteStore

// ChangeVersionStore is an optional Store capability that lets idle message
type ChangeVersionStore = model.ChangeVersionStore

const StorageProbeTimeout = model.StorageProbeTimeout

type StorageStatus = model.StorageStatus

type StorageStatusAPI = model.StorageStatusAPI

// TaskFilters narrow task reads without changing stored status or ownership.
type TaskFilters = model.TaskFilters

// TaskFilterAPI is optional; existing task interfaces retain their method sets.
type TaskFilterAPI = model.TaskFilterAPI

// TaskFilterStore optionally narrows candidates before service lease checks.
type TaskFilterStore = model.TaskFilterStore

// TaskOutcomeAPI is optional so existing TaskAPI implementations remain valid.
type TaskOutcomeAPI = model.TaskOutcomeAPI

type TaskOutcomeInput = model.TaskOutcomeInput

type TaskStatus = model.TaskStatus

const TaskPending = model.TaskPending

const TaskLeased = model.TaskLeased

const TaskCompleted = model.TaskCompleted

const TaskFailed = model.TaskFailed

const TaskCancelled = model.TaskCancelled

type Task = model.Task

type TaskClaim = model.TaskClaim

type TaskInput = model.TaskInput

// TaskReceipt records a non-secret mutation outcome. Tokens are never persisted.
type TaskReceipt = model.TaskReceipt

// OpenLease asks for a lease that lasts until the task is completed or released.
const OpenLease = model.OpenLease

type TaskAPI = model.TaskAPI

// TaskMutationAPI is optional; ordinary task calls retain their original interface.
type TaskMutationAPI = model.TaskMutationAPI

// TaskClaimRecoveryAPI is optional; recovery never extends the current lease.
type TaskClaimRecoveryAPI = model.TaskClaimRecoveryAPI

type TaskReceiptReadStore = model.TaskReceiptReadStore

type TaskReceiptWriteStore = model.TaskReceiptWriteStore

type TaskReadStore = model.TaskReadStore

type TaskWriteStore = model.TaskWriteStore

const MaxMessageBodyBytes = model.MaxMessageBodyBytes

const MaxTaskTitleBytes = model.MaxTaskTitleBytes

const MaxTaskBodyBytes = model.MaxTaskBodyBytes

const MaxReferenceKindBytes = model.MaxReferenceKindBytes

const MaxReferenceURIBytes = model.MaxReferenceURIBytes

// ContentLimits advertises the write limits in server_info.
type ContentLimits = model.ContentLimits

// Invalidf names a failed field or rule while preserving ErrInvalid. Callers
// must use safe descriptions, never credentials or rejected input values.
func Invalidf(format string, args ...any) error { return model.Invalidf(format, args...) }

// InvalidJSON describes decoder failures without exposing input values.
func InvalidJSON(err error, field string) error { return model.InvalidJSON(err, field) }

// UnknownField lists accepted keys and suggests a nearby spelling. Names come
// from the contract; request values are never included.
func UnknownField(name string, fields []string) error { return model.UnknownField(name, fields) }

// InvalidJSONFields adds the keys accepted at the failing object's level.
// InvalidJSON remains available to decoders without a request contract.
func InvalidJSONFields(err error, field string, raw json.RawMessage, target any) error {
	return model.InvalidJSONFields(err, field, raw, target)
}

const ProtocolVersion = model.ProtocolVersion

// Version can be set with -ldflags '-X github.com/krimvp/comitia.Version=...'.
var Version string

// Revision and Dirty may be stamped by source installers even with buildvcs=false.
var Revision string

// Revision and Dirty may be stamped by source installers even with buildvcs=false.
var Dirty string

type BuildInfo = model.BuildInfo

func BuildIdentity() BuildInfo { return model.BuildIdentity() }

func BuildVersion() string { return model.BuildVersion() }

type ServerInfo = model.ServerInfo

// Features returns the feature inventory compiled into this binary.
func Features() []string { return model.Features() }

type ServerInfoAPI = model.ServerInfoAPI
