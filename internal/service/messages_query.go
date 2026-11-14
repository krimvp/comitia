package service

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/schema"
)

func contextMessage(r model.MessageReadStore, cid, mid string) (model.Message, error) {
	m, err := r.Message(mid)
	if errors.Is(err, model.ErrNotFound) || err == nil && m.ContextID != cid {
		return model.Message{}, model.ErrForbidden
	}
	return m, err
}

func validateMessageQuery(r model.ReadStore, cid string, f model.MessageFilters) error {
	if f.ExcludeSelf || len(f.Schemas) != 0 {
		options, ok := r.(model.MessageReadOptionsStore)
		if !ok || !options.SupportsMessageReadOptions() {
			return model.Invalidf("message read options unsupported by store")
		}
	}
	if !model.ScopedMessageFilters(f) {
		return nil
	}
	query, ok := r.(model.MessageQueryStore)
	if !ok || !query.SupportsMessageQueries() {
		return model.Invalidf("scoped message queries unsupported by store")
	}
	for _, ref := range []struct{ field, id string }{{"reply_to", f.ReplyTo}, {"thread_id", f.ThreadID}} {
		mid := ref.id
		if mid != "" {
			if _, err := contextMessage(r, cid, mid); err != nil {
				if errors.Is(err, model.ErrForbidden) {
					return model.ReferenceError{Field: ref.field}
				}
				return err
			}
		}
	}
	return nil
}

func (s *Service) GetMessage(ctx context.Context, token, cid, mid string) (model.Message, error) {
	var out model.Message
	err := s.store.View(ctx, func(r model.ReadStore) error {
		if _, _, err := s.require(r, token, cid, model.PermissionRead); err != nil {
			return err
		}
		var err error
		out, err = contextMessage(r, cid, mid)
		return err
	})
	return out, err
}

func LookupMessage(ctx context.Context, api model.MessageAPI, token, cid, mid string) (model.Message, error) {
	query, ok := api.(model.MessageQueryAPI)
	if !ok {
		return model.Message{}, model.Invalidf("message lookup unsupported by API")
	}
	return query.GetMessage(ctx, token, cid, mid)
}

// CheckMessageQueryAPI prevents adapters from widening reads through old APIs.
func CheckMessageQueryAPI(api model.MessageAPI, f model.MessageFilters) error {
	if readOptions(f) {
		options, ok := api.(model.MessageReadOptionsAPI)
		if !ok || !options.SupportsMessageReadOptions() {
			return model.Invalidf("message read options unsupported by API")
		}
	}
	if !model.ScopedMessageFilters(f) {
		return nil
	}
	if _, ok := api.(model.MessageQueryAPI); !ok {
		return model.Invalidf("scoped message queries unsupported by API")
	}
	return nil
}

func (*Service) SupportsMessageReadOptions() bool { return true }

func readOptions(f model.MessageFilters) bool {
	return f.ExcludeSelf || f.FromCursor || len(f.Schemas) != 0
}

// resolveMessageAfter resolves an opt-in cursor start once per read or wait.
func resolveMessageAfter(ctx context.Context, api model.MessageAPI, token, cid string, after int64, f model.MessageFilters) (int64, model.MessageFilters, error) {
	if err := CheckMessageQueryAPI(api, f); err != nil {
		return after, f, err
	}
	if !f.FromCursor {
		return after, f, nil
	}
	if after != 0 {
		return after, f, model.Invalidf("from_cursor cannot be combined with after")
	}
	after, err := api.MessageCursor(ctx, token, cid, model.FilterAgentInstance(f))
	f.FromCursor = false
	return after, f, err
}

func renewedMessage(m model.Message) bool {
	if m.Envelope == nil || m.Envelope.Schema != model.TaskEventSchema {
		return false
	}
	var p schema.TaskEventPayload
	return json.Unmarshal(m.Envelope.Payload, &p) == nil && p.Kind == "renewed"
}
