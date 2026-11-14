package model

import (
	"context"
	"math"
	"time"
)

// TaskFilters narrow task reads without changing stored status or ownership.
type TaskFilters struct {
	Claimable     bool   `json:"claimable,omitempty"`
	OwnerSession  string `json:"owner_session,omitempty"`
	OwnerInstance string `json:"owner_instance,omitempty"`
	Mine          bool   `json:"mine,omitempty"`
}

// TaskFilterAPI is optional; existing task interfaces retain their method sets.
type TaskFilterAPI interface {
	ListTasksFiltered(context.Context, string, string, TaskStatus, TaskFilters, int) ([]Task, error)
	ListTasksPageFiltered(context.Context, string, string, TaskStatus, TaskFilters, string, int) (TaskPage, error)
}

// TaskFilterStore optionally narrows candidates before service lease checks.
// It applies status, claimable statuses and owner IDs, with an exclusive cursor;
// the service decides whether each lease is still live.
type TaskFilterStore interface {
	TasksFilteredAfter(string, TaskStatus, TaskFilters, string, string, int) ([]Task, error)
}

// WithInstance scopes mine to a configured process when no instance was supplied.
func (f TaskFilters) WithInstance(d InstanceDefaults) TaskFilters {
	if f.Mine && f.OwnerInstance == "" {
		if owner := d.Message(nil); owner != nil {
			f.OwnerInstance = owner.ID
		}
	}
	return f
}

// TaskOutcomeAPI is optional so existing TaskAPI implementations remain valid.
type TaskOutcomeAPI interface {
	FailTask(context.Context, string, string, TaskOutcomeInput) (Task, error)
	CancelTask(context.Context, string, string, TaskOutcomeInput) (Task, error)
}

type TaskOutcomeInput struct {
	ClaimToken   string `json:"claim_token,omitempty"`
	LeaseVersion int64  `json:"lease_version"`
	Reason       string `json:"reason"`
	OperationID  string `json:"operation_id,omitempty"`
}

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskLeased    TaskStatus = "leased"
	TaskCompleted TaskStatus = "completed"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

type Task struct {
	ID, ContextID, Title, Body string
	OutcomeReason              string `json:"outcome_reason,omitempty"`
	Status                     TaskStatus
	LeaseOwnerSessionID        string
	LeaseVersion               int64
	LeaseExpiresAt             *time.Time
	LeaseTokenHash             string `json:"-"`
	CreatedAt, UpdatedAt       time.Time
	LeaseOwnerInstance         *AgentInstance `json:"lease_owner_instance,omitempty"`
	LeaseExpired               bool           `json:"lease_expired,omitempty"`
}

type TaskClaim struct {
	Task  Task
	Token string
}

type TaskInput struct {
	Title, Body string
	OperationID string `json:"operation_id,omitempty"`
}

// TaskReceipt records a non-secret mutation outcome. Tokens are never persisted.
// Result omits Title and Body, which retries re-read from the task.
type TaskReceipt struct {
	Fingerprint string
	Result      Task
}

// Lease bounds: a lease is OpenLease or between MinLease and MaxLease, and
// adapters default an omitted lease to DefaultLease.
const (
	MinLease     = time.Second
	MaxLease     = 24 * time.Hour
	DefaultLease = 5 * time.Minute
)

// MaxTaskReasonBytes bounds the reason of a failed or cancelled task.
const MaxTaskReasonBytes = 4096

// OpenLease asks for a lease that lasts until the task is completed or released.
// Every lease is capped at the claiming session's expiry, so it is still bounded.
const OpenLease = time.Duration(math.MaxInt64)

type TaskAPI interface {
	CreateTask(context.Context, string, string, TaskInput) (Task, error)
	GetTask(context.Context, string, string) (Task, error)
	ListTasks(context.Context, string, string, TaskStatus, int) ([]Task, error)
	ClaimTask(context.Context, string, string, time.Duration, ...AgentInstance) (TaskClaim, error)
	RenewClaim(context.Context, string, string, string, int64, time.Duration) (TaskClaim, error)
	CompleteTask(context.Context, string, string, string, int64) (Task, error)
	ReleaseTask(context.Context, string, string, string, int64) (Task, error)
}

// TaskMutationAPI is optional; ordinary task calls retain their original interface.
type TaskMutationAPI interface {
	CreateTask(context.Context, string, string, TaskInput) (Task, error)
	ClaimTaskWithOperation(context.Context, string, string, time.Duration, string, ...AgentInstance) (TaskClaim, error)
	RenewClaimWithOperation(context.Context, string, string, string, int64, time.Duration, string) (TaskClaim, error)
}

// TaskClaimRecoveryAPI is optional; recovery never extends the current lease.
type TaskClaimRecoveryAPI interface {
	RecoverClaim(context.Context, string, string, int64, ...AgentInstance) (TaskClaim, error)
}

type TaskReceiptReadStore interface {
	TaskReceipt(string, string) (TaskReceipt, error)
}

type TaskReceiptWriteStore interface {
	PutTaskReceipt(string, string, TaskReceipt) error
}

type TaskReadStore interface {
	Task(string) (Task, error)
	Tasks(string, TaskStatus, int) ([]Task, error)
}

type TaskWriteStore interface {
	PutTask(Task) error
}
