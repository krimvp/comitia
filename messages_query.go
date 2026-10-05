package comitia

import (
	"context"
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
	if !scopedMessageFilters(f) {
		return nil
	}
	if _, ok := api.(MessageQueryAPI); !ok {
		return Invalidf("scoped message queries unsupported by API")
	}
	return nil
}
