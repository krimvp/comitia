package comitia

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const MaxInstanceResumeBytes = 4096

// InstancePresence is self-reported process metadata, never proof of identity or liveness.
type InstancePresence struct {
	ID        string          `json:"id"`
	Label     string          `json:"label,omitempty"`
	SessionID string          `json:"session_id"`
	ContextID string          `json:"context_id"`
	LastSeen  time.Time       `json:"last_seen"`
	Resume    json.RawMessage `json:"resume,omitempty"`
}

type PresenceAPI interface {
	ListInstances(context.Context, string, string) ([]InstancePresence, error)
	SetInstanceResume(context.Context, string, string, *AgentInstance, json.RawMessage) error
}
type PresenceReadStore interface {
	Instances(string) ([]InstancePresence, error)
}
type PresenceWriteStore interface {
	PutInstancePresence(InstancePresence) error
}

func (s *Service) ListInstances(ctx context.Context, raw, cid string) ([]InstancePresence, error) {
	var out []InstancePresence
	err := s.store.View(ctx, func(r ReadStore) error {
		if _, _, err := s.require(r, raw, cid, PermissionRead); err != nil {
			return err
		}
		var err error
		out, err = r.Instances(cid)
		return err
	})
	return out, err
}

func (s *Service) SetInstanceResume(ctx context.Context, raw, cid string, in *AgentInstance, resume json.RawMessage) error {
	instance, err := normalizeAgentInstance(in)
	if err != nil {
		return err
	}
	if instance == nil {
		return fmt.Errorf("%w: agent_instance required", ErrInvalid)
	}
	var object map[string]json.RawMessage
	if len(resume) > MaxInstanceResumeBytes || json.Unmarshal(resume, &object) != nil || object == nil {
		return fmt.Errorf("%w: resume must be a JSON object of at most %d bytes", ErrInvalid, MaxInstanceResumeBytes)
	}
	return s.store.Update(ctx, func(w WriteStore) error {
		a, _, err := s.require(w, raw, cid, PermissionRead)
		if err != nil {
			return err
		}
		return w.PutInstancePresence(InstancePresence{ID: instance.ID, Label: instance.Label, SessionID: a.session.ID, ContextID: cid, LastSeen: s.now().UTC(), Resume: resume})
	})
}

// Heartbeats use one upsert in a separate transaction after the authorized call.
// Presence failure never changes the result of a message or cursor operation.
func (s *Service) touchInstance(ctx context.Context, sid, cid string, instance *AgentInstance) {
	if instance == nil || instance.ID == "" {
		return
	}
	_ = s.store.Update(ctx, func(w WriteStore) error {
		return w.PutInstancePresence(InstancePresence{ID: instance.ID, Label: instance.Label, SessionID: sid, ContextID: cid, LastSeen: s.now().UTC()})
	})
}
