package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const MaxInstanceResumeBytes = 4096
const InstanceHeartbeatInterval = 30 * time.Second
const instanceHeartbeatBudget = 25 * time.Millisecond

// InstancePresence is self-reported process metadata, never proof of identity or liveness.
type InstancePresence struct {
	ID        string          `json:"id"`
	Label     string          `json:"label,omitempty"`
	SessionID string          `json:"session_id"`
	ContextID string          `json:"context_id"`
	LastSeen  time.Time       `json:"last_seen"`
	Resume    json.RawMessage `json:"resume,omitempty"`
	Active    bool            `json:"active"`
}

type PresenceAPI interface {
	ListInstances(context.Context, string, string) ([]InstancePresence, error)
	SetInstanceResume(context.Context, string, string, *AgentInstance, json.RawMessage) error
}
type PresenceReadStore interface {
	Instances(string, time.Time) ([]InstancePresence, error)
	InstanceLastSeen(string, string, string) (time.Time, error)
	InstanceLabel(string, string, string) (string, error)
}
type PresenceWriteStore interface {
	PutInstancePresence(InstancePresence) error
	// A failed heartbeat must not undo or poison the caller's transaction.
	TouchInstancePresence(InstancePresence) error
}

func (s *Service) ListInstances(ctx context.Context, raw, cid string) ([]InstancePresence, error) {
	var out []InstancePresence
	err := s.store.View(ctx, func(r ReadStore) error {
		if _, _, err := s.require(r, raw, cid, PermissionRead); err != nil {
			return err
		}
		var err error
		out, err = r.Instances(cid, s.now().UTC())
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
		return Invalidf("agent_instance required")
	}
	var object map[string]json.RawMessage
	if len(resume) > MaxInstanceResumeBytes || json.Unmarshal(resume, &object) != nil || object == nil {
		return Invalidf("resume must be a JSON object of at most %d bytes", MaxInstanceResumeBytes)
	}
	return s.store.Update(ctx, func(w WriteStore) error {
		a, _, err := s.require(w, raw, cid, PermissionRead)
		if err != nil {
			return err
		}
		return w.PutInstancePresence(InstancePresence{ID: instance.ID, Label: instance.Label, SessionID: a.session.ID, ContextID: cid, LastSeen: s.now().UTC(), Resume: resume})
	})
}

// Polls check freshness inside their existing View before opening a write.
func (s *Service) instanceHeartbeatDue(r ReadStore, sid, cid, instance string) bool {
	if instance == "" {
		return false
	}
	seen, err := r.InstanceLastSeen(sid, cid, instance)
	return errors.Is(err, ErrNotFound) || err == nil && !seen.After(s.now().UTC().Add(-InstanceHeartbeatInterval))
}

// Send and ack reuse their transaction; SQLite isolates the heartbeat with a savepoint.
func (s *Service) touchInstanceIn(w WriteStore, sid, cid string, instance *AgentInstance) {
	if instance == nil || instance.ID == "" {
		return
	}
	due := s.instanceHeartbeatDue(w, sid, cid, instance.ID)
	if !due && instance.Label != "" {
		label, err := w.InstanceLabel(sid, cid, instance.ID)
		due = err == nil && label != instance.Label
	}
	if !due {
		return
	}
	_ = w.TouchInstancePresence(InstancePresence{ID: instance.ID, Label: instance.Label, SessionID: sid, ContextID: cid, LastSeen: s.now().UTC()})
}

func (s *Service) touchInstance(ctx context.Context, sid, cid string, instance *AgentInstance) {
	ctx, cancel := context.WithTimeout(ctx, instanceHeartbeatBudget)
	defer cancel()
	_ = s.store.Update(ctx, func(w WriteStore) error {
		// Another reader may have recorded activity since the original View.
		s.touchInstanceIn(w, sid, cid, instance)
		return nil
	})
}
