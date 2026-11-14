package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

func boundMessages(messages []model.Message, budget int) ([]model.Message, error) {
	used := 2
	for i, m := range messages {
		raw, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		n := len(raw)
		if i > 0 {
			n++
			if used+n > model.MessageByteBudget(budget) {
				return messages[:i], nil
			}
		}
		used += n
	}
	return messages, nil
}

func messagePage(ctx context.Context, api model.MessageAPI, token, cid string, after int64, messages []model.Message, filters []model.MessageFilters) (model.MessagePage, error) {
	f, err := model.CombineMessageFilters(filters)
	if err != nil {
		return model.MessagePage{}, err
	}
	messages, err = boundMessages(messages, f.MaxBytes)
	if err != nil {
		return model.MessagePage{}, err
	}
	page := model.MessagePage{Messages: messages, NextAfter: after, ScannedThrough: after}
	if page.Messages == nil {
		page.Messages = []model.Message{}
	}
	if len(messages) == 0 {
		return page, nil
	}
	page.NextAfter = messages[len(messages)-1].Sequence
	page.ScannedThrough = page.NextAfter
	next, err := api.ReplayMessages(ctx, token, cid, page.NextAfter, 1, filters...)
	page.HasMore = len(next) != 0
	return page, err
}

func ReplayMessagePage(ctx context.Context, api model.MessageAPI, token, cid string, after int64, limit int, filters ...model.MessageFilters) (model.MessagePage, error) {
	f, err := model.CombineMessageFilters(filters)
	if err != nil {
		return model.MessagePage{}, err
	}
	if err := CheckMessageQueryAPI(api, f); err != nil {
		return model.MessagePage{}, err
	}
	if pages, ok := api.(model.MessagePageAPI); ok {
		return pages.ReplayMessagesPage(ctx, token, cid, after, limit, filters...)
	}
	after, f, err = resolveMessageAfter(ctx, api, token, cid, after, f)
	if err != nil {
		return model.MessagePage{}, err
	}
	filters = []model.MessageFilters{f}
	messages, err := api.ReplayMessages(ctx, token, cid, after, limit, filters...)
	if err != nil {
		return model.MessagePage{}, err
	}
	return messagePage(ctx, api, token, cid, after, messages, filters)
}

func WaitMessagePage(ctx context.Context, api model.MessageAPI, token, cid string, after int64, timeout time.Duration, filters ...model.MessageFilters) (model.MessagePage, error) {
	f, err := model.CombineMessageFilters(filters)
	if err != nil {
		return model.MessagePage{}, err
	}
	if err := CheckMessageQueryAPI(api, f); err != nil {
		return model.MessagePage{}, err
	}
	if pages, ok := api.(model.MessagePageAPI); ok {
		return pages.WaitMessagesPage(ctx, token, cid, after, timeout, filters...)
	}
	return waitMessagePage(ctx, api, token, cid, after, timeout, filters)
}

func (s *Service) ReplayMessagesPage(ctx context.Context, token, cid string, after int64, limit int, filters ...model.MessageFilters) (model.MessagePage, error) {
	f, err := model.CombineMessageFilters(filters)
	if err != nil {
		return model.MessagePage{}, err
	}
	after, f, err = resolveMessageAfter(ctx, s, token, cid, after, f)
	if err != nil {
		return model.MessagePage{}, err
	}
	filters = []model.MessageFilters{f}
	messages, scanned, err := s.scanMessages(ctx, token, cid, after, limit, filters)
	if err != nil {
		return model.MessagePage{}, err
	}
	page, err := messagePage(ctx, s, token, cid, after, messages, filters)
	page.ScannedThrough = scanned
	return page, err
}

func (s *Service) WaitMessagesPage(ctx context.Context, token, cid string, after int64, timeout time.Duration, filters ...model.MessageFilters) (model.MessagePage, error) {
	return waitMessagePage(ctx, s, token, cid, after, timeout, filters)
}

// The page's lookahead shares the wait budget. If it runs out after matches
// were read, a conservative continuation preserves those messages.
func waitMessagePage(ctx context.Context, api model.MessageAPI, token, cid string, after int64, timeout time.Duration, filters []model.MessageFilters) (model.MessagePage, error) {
	f, err := model.CombineMessageFilters(filters)
	if err != nil {
		return model.MessagePage{}, err
	}

	if timeout < 0 {
		return model.MessagePage{}, model.Invalidf("timeout must be nonnegative")
	}
	if timeout > model.MaxMessageWait {
		timeout = model.MaxMessageWait
	}
	readCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		readCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	after, f, err = resolveMessageAfter(readCtx, api, token, cid, after, f)
	if err != nil {
		return model.MessagePage{}, err
	}
	filters = []model.MessageFilters{f}
	scanned := after
	var messages []model.Message
	if svc, ok := api.(*Service); ok {
		messages, scanned, err = svc.waitMessagesScanned(readCtx, token, cid, after, timeout, filters...)
	} else {
		messages, err = api.WaitMessages(readCtx, token, cid, after, timeout, filters...)
	}
	if ctx.Err() != nil {
		return model.MessagePage{}, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && readCtx.Err() != nil {
			return model.MessagePage{Messages: []model.Message{}, NextAfter: after, ScannedThrough: max(after, scanned)}, nil
		}
		return model.MessagePage{}, err
	}
	page, err := messagePage(readCtx, api, token, cid, after, messages, filters)
	page.ScannedThrough = max(page.ScannedThrough, scanned)
	if ctx.Err() != nil {
		return model.MessagePage{}, ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) && readCtx.Err() != nil && len(page.Messages) > 0 {
		page.HasMore = true
		return page, nil
	}
	return page, err
}
