package service

import (
	"encoding/json"

	"github.com/krimvp/comitia/internal/schema"

	"github.com/krimvp/comitia/internal/model"
)

// putTaskEvent attributes the event to the acting session and, when that
// session holds the lease under a claimed instance, to that instance, so
// exclude_self skips an instance's own transitions. Other actors, such as an
// admin releasing or cancelling someone else's lease, stay instance-less.
func putTaskEvent(w model.MessageWriteStore, sid string, t model.Task, kind string, previous model.TaskStatus) error {
	return putTaskEventAs(w, sid, holderInstance(t, sid), t, kind, previous)
}

// holderInstance returns the instance under which sid holds t's lease, if any.
func holderInstance(t model.Task, sid string) *model.AgentInstance {
	if t.LeaseOwnerInstance == nil || t.LeaseOwnerSessionID != sid {
		return nil
	}
	return &model.AgentInstance{ID: t.LeaseOwnerInstance.ID, Label: t.LeaseOwnerInstance.Label}
}

func putTaskEventAs(w model.MessageWriteStore, sid string, instance *model.AgentInstance, t model.Task, kind string, previous model.TaskStatus) error {
	payload, err := json.Marshal(schema.TaskEventPayload{Kind: kind, PreviousStatus: previous, Status: t.Status, LeaseVersion: t.LeaseVersion, LeaseOwnerSessionID: t.LeaseOwnerSessionID, LeaseOwnerInstance: t.LeaseOwnerInstance, Reason: t.OutcomeReason})
	if err != nil {
		return err
	}
	_, err = w.PutMessage(model.Message{ID: model.NewID(), ContextID: t.ContextID, SessionID: sid, DedupeKey: "task-event." + model.NewID(), CreatedAt: t.UpdatedAt, Envelope: &model.MessageEnvelope{Schema: model.TaskEventSchema, TaskID: t.ID, Payload: payload}, AgentInstance: instance, AgentInstanceSelfReported: instance != nil})
	return err
}

func (s *Service) notifyTaskEvent(cid string) {
	if cid != "" {
		s.messageNotifications.notify(cid)
	}
}
