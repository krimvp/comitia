package comitia

import (
	"context"
	"time"

	"github.com/krimvp/comitia/internal/service"
)

// ListReferences selects reference listing without changing ContextAPI.
func ListReferences(ctx context.Context, api ContextAPI, token, cid, after string, limit int) (ReferencePage, error) {
	return service.ListReferences(ctx, api, token, cid, after, limit)
}

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

func ListTaskPage(ctx context.Context, api TaskAPI, token, cid string, status TaskStatus, after string, limit int) (TaskPage, error) {
	return service.ListTaskPage(ctx, api, token, cid, status, after, limit)
}

func ListParticipantPage(ctx context.Context, api MessageAPI, token, cid, after string, limit int) (ParticipantPage, error) {
	return service.ListParticipantPage(ctx, api, token, cid, after, limit)
}

func ReplayMessagePage(ctx context.Context, api MessageAPI, token, cid string, after int64, limit int, filters ...MessageFilters) (MessagePage, error) {
	return service.ReplayMessagePage(ctx, api, token, cid, after, limit, filters...)
}

func WaitMessagePage(ctx context.Context, api MessageAPI, token, cid string, after int64, timeout time.Duration, filters ...MessageFilters) (MessagePage, error) {
	return service.WaitMessagePage(ctx, api, token, cid, after, timeout, filters...)
}

// ListInstancePage pages APIs that predate instance paging from their
// bounded unpaged list.
func ListInstancePage(ctx context.Context, api PresenceAPI, token, cid, after string, limit int) (InstancePage, error) {
	return service.ListInstancePage(ctx, api, token, cid, after, limit)
}

type Service = service.Service

func New(cfg Config) (*Service, error) { return service.New(cfg) }

// RecoverClaim selects holder recovery without changing the original task API.
func RecoverClaim(ctx context.Context, api TaskAPI, raw, taskID string, version int64, instance ...AgentInstance) (TaskClaim, error) {
	return service.RecoverClaim(ctx, api, raw, taskID, version, instance...)
}

// CreateTaskOperation rejects recoverable calls through APIs without that capability.
func CreateTaskOperation(ctx context.Context, api TaskAPI, raw, cid string, in TaskInput) (Task, error) {
	return service.CreateTaskOperation(ctx, api, raw, cid, in)
}

// ClaimTaskOperation selects recovery only when an operation ID was supplied.
func ClaimTaskOperation(ctx context.Context, api TaskAPI, raw, taskID string, lease time.Duration, operationID string, instance ...AgentInstance) (TaskClaim, error) {
	return service.ClaimTaskOperation(ctx, api, raw, taskID, lease, operationID, instance...)
}

func RenewClaimOperation(ctx context.Context, api TaskAPI, raw, taskID, claimToken string, version int64, lease time.Duration, operationID string) (TaskClaim, error) {
	return service.RenewClaimOperation(ctx, api, raw, taskID, claimToken, version, lease, operationID)
}

func ListTasksFiltered(ctx context.Context, api TaskAPI, raw, cid string, status TaskStatus, f TaskFilters, limit int) ([]Task, error) {
	return service.ListTasksFiltered(ctx, api, raw, cid, status, f, limit)
}

func ListTaskPageFiltered(ctx context.Context, api TaskAPI, raw, cid string, status TaskStatus, f TaskFilters, after string, limit int) (TaskPage, error) {
	return service.ListTaskPageFiltered(ctx, api, raw, cid, status, f, after, limit)
}

func FailTask(ctx context.Context, api TaskAPI, raw, tid string, in TaskOutcomeInput) (Task, error) {
	return service.FailTask(ctx, api, raw, tid, in)
}

func CancelTask(ctx context.Context, api TaskAPI, raw, tid string, in TaskOutcomeInput) (Task, error) {
	return service.CancelTask(ctx, api, raw, tid, in)
}
