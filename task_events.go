package comitia

import "encoding/json"

// TaskEventSchema is reserved for transitions committed by the service.
const TaskEventSchema = "comitia.task-event.v1"

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
	return strictPayload(raw, &p)
}

func putTaskEvent(w MessageWriteStore, sid string, t Task, kind string, previous TaskStatus) error {
	payload, err := json.Marshal(TaskEventPayload{Kind: kind, PreviousStatus: previous, Status: t.Status, LeaseVersion: t.LeaseVersion, LeaseOwnerSessionID: t.LeaseOwnerSessionID, LeaseOwnerInstance: t.LeaseOwnerInstance, Reason: t.OutcomeReason})
	if err != nil {
		return err
	}
	_, err = w.PutMessage(Message{ID: id(), ContextID: t.ContextID, SessionID: sid, DedupeKey: "task-event." + id(), CreatedAt: t.UpdatedAt, Envelope: &MessageEnvelope{Schema: TaskEventSchema, TaskID: t.ID, Payload: payload}})
	return err
}

func (s *Service) notifyTaskEvent(cid string) {
	if cid != "" {
		s.messageNotifications.notify(cid)
	}
}

func terminalTaskStatus(status TaskStatus) bool {
	return status == TaskCompleted || status == TaskFailed || status == TaskCancelled
}

func validTaskStatusFilter(status TaskStatus) bool {
	return status == "" || status == TaskPending || status == TaskLeased || terminalTaskStatus(status)
}
