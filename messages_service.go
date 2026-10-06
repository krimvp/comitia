package comitia

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"strings"
)

// MaxDedupeKeyBytes bounds new dedupe keys; existing longer keys stay readable.
const MaxDedupeKeyBytes = 256

// DedupeConflictError reports a send whose dedupe scope already holds a different
// message. That message belongs to the caller's own session and instance scope;
// callers that cannot read the context get a plain ErrConflict instead.
type DedupeConflictError struct {
	MessageID string
	Sequence  int64
	Differs   []string // body, envelope, agent_instance, task_attempt
}

func (e *DedupeConflictError) Error() string {
	return fmt.Sprintf("%s: dedupe_key already used by message %s (sequence %d) with a different %s", ErrConflict, e.MessageID, e.Sequence, strings.Join(e.Differs, ", "))
}
func (e *DedupeConflictError) Unwrap() error { return ErrConflict }

func (s *Service) AppendMessage(ctx context.Context, raw, cid string, in MessageInput) (Message, error) {
	var m Message
	appended := false
	if in.DedupeKey == "" {
		return m, Invalidf("dedupe_key required")
	}
	if len(in.DedupeKey) > MaxDedupeKeyBytes {
		return m, Invalidf("dedupe_key must be at most %d bytes", MaxDedupeKeyBytes)
	}
	if in.Envelope == nil && in.Body == "" {
		return m, Invalidf("body or envelope required")
	}
	instance, err := normalizeAgentInstance(in.AgentInstance)
	if err != nil {
		return m, err
	}
	attempt := taskAttempt(in.TaskFence)
	if in.TaskFence != nil && (in.TaskFence.TaskID == "" || in.TaskFence.ClaimToken == "" || in.TaskFence.LeaseVersion < 1) {
		return m, Invalidf("task_fence requires task_id, claim_token and positive lease_version")
	}
	envelope, err := s.normalizeEnvelope(in.Envelope, in.Body)
	if err != nil {
		return m, err
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		a, _, e := s.require(w, raw, cid, PermissionWrite)
		if e != nil {
			return e
		}
		// A stance names its reply target; without (inherited) read, replies would
		// reveal whether an unreadable message exists.
		if envelope != nil && envelope.Schema == StanceSchema {
			if _, _, e := s.requireAuthenticated(w, a, cid, PermissionRead); e != nil {
				return e
			}
		}
		if in.TaskFence != nil {
			if _, _, e := s.require(w, raw, cid, PermissionClaim); e != nil {
				return e
			}
		}
		instanceID := ""
		if instance != nil {
			instanceID = instance.ID
		}
		old, e := w.MessageByDedupe(cid, a.session.ID, in.DedupeKey, instanceID)
		if e == nil {
			if differs := dedupeDiffers(old, in.Body, envelope, instance, attempt); len(differs) > 0 {
				// Identify the message only to a caller that can currently read it.
				if _, _, e := s.requireAuthenticated(w, a, cid, PermissionRead); e != nil {
					if errors.Is(e, ErrForbidden) {
						return ErrConflict
					}
					return e
				}
				return &DedupeConflictError{MessageID: old.ID, Sequence: old.Sequence, Differs: differs}
			}
			m = old
			s.touchInstanceIn(w, a.session.ID, cid, instance)
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		if e := s.validateTaskFence(w, raw, cid, envelope, in.TaskFence); e != nil {
			return e
		}
		if e := s.requireTaskStatusRead(w, raw, cid, envelope); e != nil {
			return e
		}
		if e := s.validateEnvelopeReferences(w, cid, envelope); e != nil {
			return e
		}
		if e := s.validateProtocolReferences(w, cid, a.session.ID, envelope); e != nil {
			return e
		}
		m = Message{TaskAttempt: attempt, ID: id(), ContextID: cid, SessionID: a.session.ID, DedupeKey: in.DedupeKey, Body: in.Body, CreatedAt: s.now().UTC(), Envelope: envelope, AgentInstance: instance, AgentInstanceSelfReported: instance != nil}
		m, e = w.PutMessage(m)
		appended = e == nil
		if e == nil {
			s.touchInstanceIn(w, a.session.ID, cid, instance)
		}
		return e
	})
	if e == nil && appended {
		s.messageNotifications.notify(cid)
	}
	return m, e
}
func (s *Service) ReplayMessages(ctx context.Context, raw, cid string, after int64, limit int, filters ...MessageFilters) ([]Message, error) {
	messages, _, err := s.scanMessages(ctx, raw, cid, after, limit, filters)
	return messages, err
}

func (s *Service) AckMessages(ctx context.Context, raw, cid string, seq int64, instances ...string) error {
	instance, err := cursorInstance(instances)
	if err != nil {
		return err
	}
	if seq < 0 {
		return Invalidf("sequence must be nonnegative")
	}
	return s.store.Update(ctx, func(w WriteStore) error {
		a, _, err := s.require(w, raw, cid, PermissionRead)
		if err != nil {
			return err
		}
		if err := w.Ack(a.session.ID, cid, seq, s.now().UTC(), instance); err != nil {
			return err
		}
		s.touchInstanceIn(w, a.session.ID, cid, &AgentInstance{ID: instance})
		return nil
	})
}
func (s *Service) MessageCursor(ctx context.Context, raw, cid string, instances ...string) (int64, error) {
	instance, err := cursorInstance(instances)
	if err != nil {
		return 0, err
	}
	var sid string
	var heartbeat bool
	var cursor int64
	e := s.store.View(ctx, func(r ReadStore) error {
		a, _, e := s.require(r, raw, cid, PermissionRead)
		if e != nil {
			return e
		}
		sid = a.session.ID
		cursor, e = r.AckCursor(a.session.ID, cid, instance)
		if e == nil {
			heartbeat = s.instanceHeartbeatDue(r, sid, cid, instance)
		}
		return e
	})
	if e == nil && heartbeat {
		s.touchInstance(ctx, sid, cid, &AgentInstance{ID: instance})
	}
	return cursor, e
}

// TaskFence authenticates a result against the current live lease in the append transaction.
type TaskFence struct {
	TaskID       string `json:"task_id"`
	ClaimToken   string `json:"claim_token"`
	LeaseVersion int64  `json:"lease_version"`
}

// TaskAttempt is server-validated provenance. SessionID and Envelope.ReplyTo on
// the containing message identify the authenticated author and request.
type TaskAttempt struct {
	TaskID         string `json:"task_id"`
	LeaseVersion   int64  `json:"lease_version"`
	ClaimTokenHash string `json:"-"`
}

func taskAttempt(f *TaskFence) *TaskAttempt {
	if f == nil {
		return nil
	}
	return &TaskAttempt{TaskID: f.TaskID, LeaseVersion: f.LeaseVersion, ClaimTokenHash: tokenHash(f.ClaimToken)}
}

// dedupeDiffers names the differing parts without exposing their values.
func dedupeDiffers(old Message, body string, envelope *MessageEnvelope, instance *AgentInstance, attempt *TaskAttempt) []string {
	var differs []string
	for _, part := range []struct {
		name string
		same bool
	}{
		{"body", old.Body == body}, {"envelope", sameEnvelope(old.Envelope, envelope)},
		{"agent_instance", sameAgentInstance(old.AgentInstance, instance)}, {"task_attempt", sameTaskAttempt(old.TaskAttempt, attempt)},
	} {
		if !part.same {
			differs = append(differs, part.name)
		}
	}
	return differs
}
func sameTaskAttempt(a, b *TaskAttempt) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.TaskID == b.TaskID && a.LeaseVersion == b.LeaseVersion && subtle.ConstantTimeCompare([]byte(a.ClaimTokenHash), []byte(b.ClaimTokenHash)) == 1
}
func (s *Service) validateTaskFence(w WriteStore, raw, cid string, envelope *MessageEnvelope, f *TaskFence) error {
	if f == nil {
		return nil
	}
	if envelope == nil || envelope.TaskID != f.TaskID {
		return Invalidf("task_fence must match envelope.task_id")
	}
	a, _, e := s.require(w, raw, cid, PermissionClaim)
	if e != nil {
		return e
	}
	t, e := w.Task(f.TaskID)
	if errors.Is(e, ErrNotFound) {
		return ErrForbidden
	}
	if e != nil {
		return e
	}
	if t.ContextID != cid {
		return ErrForbidden
	}
	now := s.now().UTC()
	if t.Status != TaskLeased || t.LeaseOwnerSessionID != a.session.ID || t.LeaseVersion != f.LeaseVersion || t.LeaseExpiresAt == nil || !t.LeaseExpiresAt.After(now) || subtle.ConstantTimeCompare([]byte(t.LeaseTokenHash), []byte(tokenHash(f.ClaimToken))) != 1 {
		return ErrStaleClaim
	}
	return nil
}
