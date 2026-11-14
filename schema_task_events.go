package comitia

import (
	"encoding/json"

	"github.com/krimvp/comitia/internal/model"
)

type TaskEventPayload struct {
	Kind                string         `json:"kind"`
	PreviousStatus      TaskStatus     `json:"previous_status,omitempty"`
	Status              TaskStatus     `json:"status"`
	LeaseVersion        int64          `json:"lease_version"`
	LeaseOwnerSessionID string         `json:"lease_owner_session_id,omitempty"`
	LeaseOwnerInstance  *AgentInstance `json:"lease_owner_instance,omitempty"`
	Reason              string         `json:"reason,omitempty"`
}

func validTaskEvent(raw json.RawMessage) error {
	var p TaskEventPayload
	if err := strictPayload(raw, &p); err != nil {
		return err
	}
	switch p.Kind {
	case "created", "claimed", "renewed", "released", "completed", "failed", "cancelled":
	default:
		return Invalidf("kind must be created, claimed, renewed, released, completed, failed or cancelled")
	}
	if p.Status == "" || !validTaskStatusFilter(p.Status) || !validTaskStatusFilter(p.PreviousStatus) || p.LeaseVersion < 0 {
		return Invalidf("status and previous_status must be task statuses; lease_version must be nonnegative")
	}
	if p.LeaseOwnerInstance != nil && !model.ValidInstanceID(p.LeaseOwnerInstance.ID) {
		return Invalidf("lease_owner_instance.id must be a valid instance ID")
	}
	return nil
}

func terminalTaskStatus(status TaskStatus) bool {
	return status == TaskCompleted || status == TaskFailed || status == TaskCancelled
}

func validTaskStatusFilter(status TaskStatus) bool {
	return status == "" || status == TaskPending || status == TaskLeased || terminalTaskStatus(status)
}
