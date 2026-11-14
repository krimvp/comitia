package comitia

import (
	"context"
	"encoding/json"
	"errors"
)

func contextMessage(r MessageReadStore, cid, mid string) (Message, error) {
	m, err := r.Message(mid)
	if errors.Is(err, ErrNotFound) || err == nil && m.ContextID != cid {
		return Message{}, ErrForbidden
	}
	return m, err
}

func validateMessageQuery(r ReadStore, cid string, f MessageFilters) error {
	if f.ExcludeSelf || len(f.Schemas) != 0 {
		options, ok := r.(MessageReadOptionsStore)
		if !ok || !options.SupportsMessageReadOptions() {
			return Invalidf("message read options unsupported by store")
		}
	}
	if !scopedMessageFilters(f) {
		return nil
	}
	query, ok := r.(MessageQueryStore)
	if !ok || !query.SupportsMessageQueries() {
		return Invalidf("scoped message queries unsupported by store")
	}
	for _, ref := range []struct{ field, id string }{{"reply_to", f.ReplyTo}, {"thread_id", f.ThreadID}} {
		mid := ref.id
		if mid != "" {
			if _, err := contextMessage(r, cid, mid); err != nil {
				if errors.Is(err, ErrForbidden) {
					return referenceError{ref.field}
				}
				return err
			}
		}
	}
	return nil
}

func (s *Service) GetMessage(ctx context.Context, token, cid, mid string) (Message, error) {
	var out Message
	err := s.store.View(ctx, func(r ReadStore) error {
		if _, _, err := s.require(r, token, cid, PermissionRead); err != nil {
			return err
		}
		var err error
		out, err = contextMessage(r, cid, mid)
		return err
	})
	return out, err
}

func LookupMessage(ctx context.Context, api MessageAPI, token, cid, mid string) (Message, error) {
	query, ok := api.(MessageQueryAPI)
	if !ok {
		return Message{}, Invalidf("message lookup unsupported by API")
	}
	return query.GetMessage(ctx, token, cid, mid)
}

// CheckMessageQueryAPI prevents adapters from widening reads through old APIs.
func CheckMessageQueryAPI(api MessageAPI, f MessageFilters) error {
	if readOptions(f) {
		options, ok := api.(MessageReadOptionsAPI)
		if !ok || !options.SupportsMessageReadOptions() {
			return Invalidf("message read options unsupported by API")
		}
	}
	if !scopedMessageFilters(f) {
		return nil
	}
	if _, ok := api.(MessageQueryAPI); !ok {
		return Invalidf("scoped message queries unsupported by API")
	}
	return nil
}

func (*Service) SupportsMessageReadOptions() bool { return true }

func readOptions(f MessageFilters) bool {
	return f.ExcludeSelf || f.FromCursor || len(f.Schemas) != 0
}

// resolveMessageAfter resolves an opt-in cursor start once per read or wait.
func resolveMessageAfter(ctx context.Context, api MessageAPI, token, cid string, after int64, f MessageFilters) (int64, MessageFilters, error) {
	if err := CheckMessageQueryAPI(api, f); err != nil {
		return after, f, err
	}
	if !f.FromCursor {
		return after, f, nil
	}
	if after != 0 {
		return after, f, Invalidf("from_cursor cannot be combined with after")
	}
	after, err := api.MessageCursor(ctx, token, cid, filterAgentInstance(f))
	f.FromCursor = false
	return after, f, err
}

func renewedMessage(m Message) bool {
	if m.Envelope == nil || m.Envelope.Schema != TaskEventSchema {
		return false
	}
	var p TaskEventPayload
	return json.Unmarshal(m.Envelope.Payload, &p) == nil && p.Kind == "renewed"
}
