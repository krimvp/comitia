package comitia

import (
	"encoding/json"
)

// putTaskEvent attributes the event to the acting session and, when that
// session holds the lease under a claimed instance, to that instance, so
// exclude_self skips an instance's own transitions. Other actors, such as an
// admin releasing or cancelling someone else's lease, stay instance-less.
func putTaskEvent(w MessageWriteStore, sid string, t Task, kind string, previous TaskStatus) error {
	return putTaskEventAs(w, sid, holderInstance(t, sid), t, kind, previous)
}

// holderInstance returns the instance under which sid holds t's lease, if any.
func holderInstance(t Task, sid string) *AgentInstance {
	if t.LeaseOwnerInstance == nil || t.LeaseOwnerSessionID != sid {
		return nil
	}
	return &AgentInstance{ID: t.LeaseOwnerInstance.ID, Label: t.LeaseOwnerInstance.Label}
}

func putTaskEventAs(w MessageWriteStore, sid string, instance *AgentInstance, t Task, kind string, previous TaskStatus) error {
	payload, err := json.Marshal(TaskEventPayload{Kind: kind, PreviousStatus: previous, Status: t.Status, LeaseVersion: t.LeaseVersion, LeaseOwnerSessionID: t.LeaseOwnerSessionID, LeaseOwnerInstance: t.LeaseOwnerInstance, Reason: t.OutcomeReason})
	if err != nil {
		return err
	}
	_, err = w.PutMessage(Message{ID: id(), ContextID: t.ContextID, SessionID: sid, DedupeKey: "task-event." + id(), CreatedAt: t.UpdatedAt, Envelope: &MessageEnvelope{Schema: TaskEventSchema, TaskID: t.ID, Payload: payload}, AgentInstance: instance, AgentInstanceSelfReported: instance != nil})
	return err
}

func (s *Service) notifyTaskEvent(cid string) {
	if cid != "" {
		s.messageNotifications.notify(cid)
	}
}
