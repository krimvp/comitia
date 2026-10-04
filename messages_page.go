package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const DefaultMessageBytes = 1 << 20
const MaxMessageBytes = 4 << 20

// MessagePage preserves whole messages. One message may exceed the soft byte
// budget so a reader can always advance. NextAfter is exclusive, never an ack.
type MessagePage struct {
	Messages  []Message `json:"messages"`
	NextAfter int64     `json:"next_after"`
	HasMore   bool      `json:"has_more"`
}

// MessagePageAPI is optional so existing embedded implementations remain valid.
type MessagePageAPI interface {
	ReplayMessagesPage(context.Context, string, string, int64, int, ...MessageFilters) (MessagePage, error)
	WaitMessagesPage(context.Context, string, string, int64, time.Duration, ...MessageFilters) (MessagePage, error)
}

func messageByteBudget(n int) int {
	if n == 0 {
		return DefaultMessageBytes
	}
	return n
}

func boundMessages(messages []Message, budget int) ([]Message, error) {
	used := 2
	for i, m := range messages {
		raw, err := json.Marshal(m)
		if err != nil {
			return nil, err
		}
		n := len(raw)
		if i > 0 {
			n++
			if used+n > messageByteBudget(budget) {
				return messages[:i], nil
			}
		}
		used += n
	}
	return messages, nil
}

func messagePage(ctx context.Context, api MessageAPI, token, cid string, after int64, messages []Message, filters []MessageFilters) (MessagePage, error) {
	f, err := messageFilters(filters)
	if err != nil {
		return MessagePage{}, err
	}
	messages, err = boundMessages(messages, f.MaxBytes)
	if err != nil {
		return MessagePage{}, err
	}
	page := MessagePage{Messages: messages, NextAfter: after}
	if page.Messages == nil {
		page.Messages = []Message{}
	}
	if len(messages) == 0 {
		return page, nil
	}
	page.NextAfter = messages[len(messages)-1].Sequence
	next, err := api.ReplayMessages(ctx, token, cid, page.NextAfter, 1, filters...)
	page.HasMore = len(next) != 0
	return page, err
}

func ReplayMessagePage(ctx context.Context, api MessageAPI, token, cid string, after int64, limit int, filters ...MessageFilters) (MessagePage, error) {
	if pages, ok := api.(MessagePageAPI); ok {
		return pages.ReplayMessagesPage(ctx, token, cid, after, limit, filters...)
	}
	messages, err := api.ReplayMessages(ctx, token, cid, after, limit, filters...)
	if err != nil {
		return MessagePage{}, err
	}
	return messagePage(ctx, api, token, cid, after, messages, filters)
}

func WaitMessagePage(ctx context.Context, api MessageAPI, token, cid string, after int64, timeout time.Duration, filters ...MessageFilters) (MessagePage, error) {
	if pages, ok := api.(MessagePageAPI); ok {
		return pages.WaitMessagesPage(ctx, token, cid, after, timeout, filters...)
	}
	return waitMessagePage(ctx, api, token, cid, after, timeout, filters)
}

func (s *Service) ReplayMessagesPage(ctx context.Context, token, cid string, after int64, limit int, filters ...MessageFilters) (MessagePage, error) {
	messages, err := s.ReplayMessages(ctx, token, cid, after, limit, filters...)
	if err != nil {
		return MessagePage{}, err
	}
	return messagePage(ctx, s, token, cid, after, messages, filters)
}

func (s *Service) WaitMessagesPage(ctx context.Context, token, cid string, after int64, timeout time.Duration, filters ...MessageFilters) (MessagePage, error) {
	return waitMessagePage(ctx, s, token, cid, after, timeout, filters)
}

// The page's lookahead shares the wait budget. If it runs out after matches
// were read, a conservative continuation preserves those messages.
func waitMessagePage(ctx context.Context, api MessageAPI, token, cid string, after int64, timeout time.Duration, filters []MessageFilters) (MessagePage, error) {
	if timeout < 0 {
		return MessagePage{}, Invalidf("timeout must be nonnegative")
	}
	if timeout > MaxMessageWait {
		timeout = MaxMessageWait
	}
	readCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		readCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	messages, err := api.WaitMessages(readCtx, token, cid, after, timeout, filters...)
	if ctx.Err() != nil {
		return MessagePage{}, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && readCtx.Err() != nil {
			return MessagePage{Messages: []Message{}, NextAfter: after}, nil
		}
		return MessagePage{}, err
	}
	page, err := messagePage(readCtx, api, token, cid, after, messages, filters)
	if ctx.Err() != nil {
		return MessagePage{}, ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) && readCtx.Err() != nil && len(page.Messages) > 0 {
		page.HasMore = true
		return page, nil
	}
	return page, err
}
