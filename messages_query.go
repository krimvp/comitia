package comitia

import (
	"context"
	"encoding/json"
	"errors"
)

// MessageQueryAPI opts into lookup and all scoped MessageFilters.
// MessageAPI's original method set is unchanged.
type MessageQueryAPI interface {
	GetMessage(context.Context, string, string, string) (Message, error)
}

// MessageQueryStore opts into the ReplyTo, ThreadID and FromSession filters.
// An older store must never silently return an unfiltered conversation.
type MessageQueryStore interface {
	SupportsMessageQueries() bool
}

func (*sqlStore) SupportsMessageQueries() bool { return true }

func scopedMessageFilters(f MessageFilters) bool {
	return f.ReplyTo != "" || f.ThreadID != "" || f.FromSession != ""
}

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
	for _, mid := range []string{f.ReplyTo, f.ThreadID} {
		if mid != "" {
			if _, err := contextMessage(r, cid, mid); err != nil {
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

// MessageReadOptionsAPI opts into self exclusion, cursor starts and schema lists.
// Older APIs must not silently ignore these read options.
type MessageReadOptionsAPI interface {
	SupportsMessageReadOptions() bool
}

// MessageReadOptionsStore opts into the ExcludeSelf and Schemas predicates.
type MessageReadOptionsStore interface {
	SupportsMessageReadOptions() bool
}

func (*Service) SupportsMessageReadOptions() bool  { return true }
func (*sqlStore) SupportsMessageReadOptions() bool { return true }

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
	after, err := api.MessageCursor(ctx, token, cid, f.agentInstance())
	f.FromCursor = false
	return after, f, err
}

func (f MessageFilters) includesRenewals() bool {
	if f.Schema == TaskEventSchema {
		return true
	}
	for _, schema := range f.Schemas {
		if schema == TaskEventSchema {
			return true
		}
	}
	return false
}

func renewedMessage(m Message) bool {
	if m.Envelope == nil || m.Envelope.Schema != TaskEventSchema {
		return false
	}
	var p TaskEventPayload
	return json.Unmarshal(m.Envelope.Payload, &p) == nil && p.Kind == "renewed"
}
