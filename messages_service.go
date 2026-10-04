package comitia

import (
	"context"
	"errors"
)

func (s *Service) AppendMessage(ctx context.Context, raw, cid string, in MessageInput) (Message, error) {
	var m Message
	appended := false
	if in.DedupeKey == "" {
		return m, Invalidf("dedupe_key required")
	}
	if in.Envelope == nil && in.Body == "" {
		return m, Invalidf("body or envelope required")
	}
	instance, err := normalizeAgentInstance(in.AgentInstance)
	if err != nil {
		return m, err
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
		instanceID := ""
		if instance != nil {
			instanceID = instance.ID
		}
		old, e := w.MessageByDedupe(cid, a.session.ID, in.DedupeKey, instanceID)
		if e == nil {
			if old.Body != in.Body || !sameEnvelope(old.Envelope, envelope) || !sameAgentInstance(old.AgentInstance, instance) {
				return ErrConflict
			}
			m = old
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		if e := s.requireTaskStatusRead(w, raw, cid, envelope); e != nil {
			return e
		}
		if e := s.validateEnvelopeReferences(w, cid, envelope); e != nil {
			return e
		}
		m = Message{ID: id(), ContextID: cid, SessionID: a.session.ID, DedupeKey: in.DedupeKey, Body: in.Body, CreatedAt: s.now().UTC(), Envelope: envelope, AgentInstance: instance, AgentInstanceSelfReported: instance != nil}
		m, e = w.PutMessage(m)
		appended = e == nil
		return e
	})
	if e == nil && appended {
		s.messageNotifications.notify(cid)
	}
	return m, e
}
func (s *Service) ReplayMessages(ctx context.Context, raw, cid string, after int64, limit int, filters ...MessageFilters) ([]Message, error) {
	f, err := messageFilters(filters)
	if err != nil {
		return nil, err
	}
	var out []Message
	if after < 0 {
		return nil, Invalidf("after must be nonnegative")
	}
	if limit < 1 || limit > 1000 {
		return nil, Invalidf("limit must be between 1 and 1000")
	}
	e := s.store.View(ctx, func(r ReadStore) error {
		a, _, e := s.require(r, raw, cid, PermissionRead)
		if e != nil {
			return e
		}
		out, e = r.Messages(cid, a.session.ID, after, limit, f)
		return e
	})
	return out, e
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
		a, _, e := s.require(w, raw, cid, PermissionRead)
		if e != nil {
			return e
		}
		return w.Ack(a.session.ID, cid, seq, s.now().UTC(), instance)
	})
}
func (s *Service) MessageCursor(ctx context.Context, raw, cid string, instances ...string) (int64, error) {
	instance, err := cursorInstance(instances)
	if err != nil {
		return 0, err
	}
	var cursor int64
	e := s.store.View(ctx, func(r ReadStore) error {
		a, _, e := s.require(r, raw, cid, PermissionRead)
		if e != nil {
			return e
		}
		cursor, e = r.AckCursor(a.session.ID, cid, instance)
		return e
	})
	return cursor, e
}
