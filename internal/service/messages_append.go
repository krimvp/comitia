package service

import (
	"context"
	"crypto/subtle"
	"errors"

	"github.com/krimvp/comitia/internal/model"
)

func (s *Service) AppendMessage(ctx context.Context, raw, cid string, in model.MessageInput) (model.Message, error) {
	var m model.Message
	appended := false
	if in.DedupeKey == "" {
		return m, model.Invalidf("dedupe_key required")
	}
	if len(in.DedupeKey) > model.MaxDedupeKeyBytes {
		return m, model.Invalidf("dedupe_key must be at most %d bytes", model.MaxDedupeKeyBytes)
	}
	if in.Envelope == nil && in.Body == "" {
		return m, model.Invalidf("body or envelope required")
	}
	if e := model.ValidText("body", in.Body, model.MaxMessageBodyBytes); e != nil {
		return m, e
	}
	instance, err := model.NormalizeAgentInstance(in.AgentInstance)
	if err != nil {
		return m, err
	}
	attempt := taskAttempt(in.TaskFence)
	if in.TaskFence != nil && (in.TaskFence.TaskID == "" || in.TaskFence.ClaimToken == "" || in.TaskFence.LeaseVersion < 1) {
		return m, model.Invalidf("task_fence requires task_id, claim_token and positive lease_version")
	}
	envelope, err := s.normalizeMessageEnvelope(in)
	if err != nil {
		return m, err
	}
	references := envelope
	if envelope != nil {
		// Errors name the caller's indexes; persistence and dedupe use canonical order.
		copy := *envelope
		recipientSessionIDs := in.RecipientSessionIDs
		if len(recipientSessionIDs) == 0 && in.Envelope != nil {
			recipientSessionIDs = in.Envelope.RecipientSessionIDs
		}
		copy.RecipientSessionIDs = append([]string(nil), recipientSessionIDs...)
		references = &copy
	}
	e := s.store.Update(ctx, func(w model.WriteStore) error {
		a, _, e := s.require(w, raw, cid, model.PermissionWrite)
		if e != nil {
			return e
		}
		if e := s.validateMessageReferenceAccess(w, a, cid, envelope); e != nil {
			return e
		}
		if in.TaskFence != nil {
			if _, _, e := s.require(w, raw, cid, model.PermissionClaim); e != nil {
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
				if _, _, e := s.requireAuthenticated(w, a, cid, model.PermissionRead); e != nil {
					if errors.Is(e, model.ErrForbidden) {
						return model.ErrConflict
					}
					return e
				}
				return &model.DedupeConflictError{MessageID: old.ID, Sequence: old.Sequence, Differs: differs}
			}
			m = old
			s.touchInstanceIn(w, a.session.ID, cid, instance)
			return nil
		}
		if !errors.Is(e, model.ErrNotFound) {
			return e
		}
		if e := s.validateTaskFence(w, raw, cid, envelope, in.TaskFence); e != nil {
			return e
		}
		if e := s.requireTaskStatusRead(w, raw, cid, envelope); e != nil {
			return e
		}
		if e := s.validateEnvelopeReferences(w, cid, references); e != nil {
			return e
		}
		if e := s.validateProtocolReferences(w, cid, a.session.ID, envelope); e != nil {
			return e
		}
		m = model.Message{TaskAttempt: attempt, ID: model.NewID(), ContextID: cid, SessionID: a.session.ID, DedupeKey: in.DedupeKey, Body: in.Body, CreatedAt: s.now().UTC(), Envelope: envelope, AgentInstance: instance, AgentInstanceSelfReported: instance != nil}
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

// Routed plain notes use the existing discussion representation, including its
// validation, recipient indexes and dedupe identity.
func (s *Service) normalizeMessageEnvelope(in model.MessageInput) (*model.MessageEnvelope, error) {
	if len(in.RecipientSessionIDs) == 0 && len(in.RecipientInstances) == 0 {
		return s.normalizeEnvelope(in.Envelope, in.Body)
	}
	env := model.MessageEnvelope{Schema: "comitia.discussion.v1", Payload: []byte(`{}`)}
	if in.Envelope != nil {
		env = *in.Envelope
	}
	if len(in.RecipientSessionIDs) > 0 {
		if len(env.RecipientSessionIDs) > 0 {
			return nil, model.Invalidf("recipient_session_ids must be supplied at top level or in envelope, not both")
		}
		env.RecipientSessionIDs = in.RecipientSessionIDs
	}
	if len(in.RecipientInstances) > 0 {
		if len(env.RecipientInstances) > 0 {
			return nil, model.Invalidf("recipient_instances must be supplied at top level or in envelope, not both")
		}
		env.RecipientInstances = in.RecipientInstances
	}
	return s.normalizeEnvelope(&env, in.Body)
}

func (s *Service) ReplayMessages(ctx context.Context, raw, cid string, after int64, limit int, filters ...model.MessageFilters) ([]model.Message, error) {
	messages, _, err := s.scanMessages(ctx, raw, cid, after, limit, filters)
	return messages, err
}

func (s *Service) AckMessages(ctx context.Context, raw, cid string, seq int64, instances ...string) error {
	instance, err := model.CursorInstance(instances)
	if err != nil {
		return err
	}
	if seq < 0 {
		return model.Invalidf("sequence must be nonnegative")
	}
	return s.store.Update(ctx, func(w model.WriteStore) error {
		a, _, err := s.require(w, raw, cid, model.PermissionRead)
		if err != nil {
			return err
		}
		if err := w.Ack(a.session.ID, cid, seq, s.now().UTC(), instance); err != nil {
			return err
		}
		s.touchInstanceIn(w, a.session.ID, cid, &model.AgentInstance{ID: instance})
		return nil
	})
}

func (s *Service) MessageCursor(ctx context.Context, raw, cid string, instances ...string) (int64, error) {
	instance, err := model.CursorInstance(instances)
	if err != nil {
		return 0, err
	}
	var sid string
	var heartbeat bool
	var cursor int64
	e := s.store.View(ctx, func(r model.ReadStore) error {
		a, _, e := s.require(r, raw, cid, model.PermissionRead)
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
		s.touchInstance(ctx, sid, cid, &model.AgentInstance{ID: instance})
	}
	return cursor, e
}

func taskAttempt(f *model.TaskFence) *model.TaskAttempt {
	if f == nil {
		return nil
	}
	return &model.TaskAttempt{TaskID: f.TaskID, LeaseVersion: f.LeaseVersion, ClaimTokenHash: tokenHash(f.ClaimToken)}
}

// dedupeDiffers names the differing parts without exposing their values.
func dedupeDiffers(old model.Message, body string, envelope *model.MessageEnvelope, instance *model.AgentInstance, attempt *model.TaskAttempt) []string {
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

func sameTaskAttempt(a, b *model.TaskAttempt) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.TaskID == b.TaskID && a.LeaseVersion == b.LeaseVersion && subtle.ConstantTimeCompare([]byte(a.ClaimTokenHash), []byte(b.ClaimTokenHash)) == 1
}

func (s *Service) validateTaskFence(w model.WriteStore, raw, cid string, envelope *model.MessageEnvelope, f *model.TaskFence) error {
	if f == nil {
		return nil
	}
	if envelope == nil || envelope.TaskID != f.TaskID {
		return model.Invalidf("task_fence must match envelope.task_id")
	}
	a, _, e := s.require(w, raw, cid, model.PermissionClaim)
	if e != nil {
		return e
	}
	t, e := w.Task(f.TaskID)
	if errors.Is(e, model.ErrNotFound) {
		return model.ErrForbidden
	}
	if e != nil {
		return e
	}
	if t.ContextID != cid {
		return model.ErrForbidden
	}
	now := s.now().UTC()
	if t.Status != model.TaskLeased || t.LeaseOwnerSessionID != a.session.ID || t.LeaseVersion != f.LeaseVersion || t.LeaseExpiresAt == nil || !t.LeaseExpiresAt.After(now) || subtle.ConstantTimeCompare([]byte(t.LeaseTokenHash), []byte(tokenHash(f.ClaimToken))) != 1 {
		return model.ErrStaleClaim
	}
	return nil
}
