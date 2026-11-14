package schema

import (
	"encoding/json"

	"github.com/krimvp/comitia/internal/model"
)

type TaskEventPayload struct {
	Kind                string               `json:"kind"`
	PreviousStatus      model.TaskStatus     `json:"previous_status,omitempty"`
	Status              model.TaskStatus     `json:"status"`
	LeaseVersion        int64                `json:"lease_version"`
	LeaseOwnerSessionID string               `json:"lease_owner_session_id,omitempty"`
	LeaseOwnerInstance  *model.AgentInstance `json:"lease_owner_instance,omitempty"`
	Reason              string               `json:"reason,omitempty"`
}

func validTaskEvent(raw json.RawMessage) error {
	var p TaskEventPayload
	if err := StrictPayload(raw, &p); err != nil {
		return err
	}
	switch p.Kind {
	case "created", "claimed", "renewed", "released", "completed", "failed", "cancelled":
	default:
		return model.Invalidf("kind must be created, claimed, renewed, released, completed, failed or cancelled")
	}
	if p.Status == "" || !ValidTaskStatusFilter(p.Status) || !ValidTaskStatusFilter(p.PreviousStatus) || p.LeaseVersion < 0 {
		return model.Invalidf("status and previous_status must be task statuses; lease_version must be nonnegative")
	}
	if p.LeaseOwnerInstance != nil && !model.ValidInstanceID(p.LeaseOwnerInstance.ID) {
		return model.Invalidf("lease_owner_instance.id must be a valid instance ID")
	}
	return nil
}

func TerminalTaskStatus(status model.TaskStatus) bool {
	return status == model.TaskCompleted || status == model.TaskFailed || status == model.TaskCancelled
}

func ValidTaskStatusFilter(status model.TaskStatus) bool {
	return status == "" || status == model.TaskPending || status == model.TaskLeased || TerminalTaskStatus(status)
}
