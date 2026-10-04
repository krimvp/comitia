package comitia

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"
)

const MaxMessageWait = 60 * time.Second

func messageFilters(filters []MessageFilters) (MessageFilters, error) {
	if len(filters) > 1 {
		return MessageFilters{}, ErrInvalid
	}
	var f MessageFilters
	if len(filters) == 1 {
		f = filters[0]
	}
	if f.ForInstance != "" && !validInstanceID(f.ForInstance) || f.FromInstance != "" && !validInstanceID(f.FromInstance) {
		return f, fmt.Errorf("%w: invalid instance filter", ErrInvalid)
	}
	return f, nil
}

// MessageWaitTimeout converts wire seconds without overflowing a duration.
func MessageWaitTimeout(seconds float64) (time.Duration, error) {
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0, fmt.Errorf("%w: invalid wait timeout", ErrInvalid)
	}
	if seconds >= MaxMessageWait.Seconds() {
		return MaxMessageWait, nil
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

type messageNotification struct {
	changed chan struct{}
	waiters int
}
type messageNotifications struct {
	mu       sync.Mutex
	contexts map[string]*messageNotification
}

func (n *messageNotifications) subscribe(cid string) (*messageNotification, func()) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.contexts == nil {
		n.contexts = make(map[string]*messageNotification)
	}
	v := n.contexts[cid]
	if v == nil {
		v = &messageNotification{changed: make(chan struct{})}
		n.contexts[cid] = v
	}
	v.waiters++
	return v, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		v.waiters--
		if v.waiters == 0 {
			delete(n.contexts, cid)
		}
	}
}
func (n *messageNotifications) channel(v *messageNotification) <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return v.changed
}
func (n *messageNotifications) notify(cid string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if v := n.contexts[cid]; v != nil {
		close(v.changed)
		v.changed = make(chan struct{})
	}
}

// WaitMessages returns up to 100 matches, or an empty slice on timeout. It
// subscribes before reading to avoid losing an append between read and wait.
// Each read rechecks authorization; no transaction remains open while waiting.
func (s *Service) WaitMessages(ctx context.Context, raw, cid string, after int64, timeout time.Duration, filters ...MessageFilters) ([]Message, error) {
	if timeout < 0 {
		return nil, ErrInvalid
	}
	if timeout > MaxMessageWait {
		timeout = MaxMessageWait
	}
	v, unsubscribe := s.messageNotifications.subscribe(cid)
	defer unsubscribe()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		changed := s.messageNotifications.channel(v)
		messages, err := s.ReplayMessages(ctx, raw, cid, after, 100, filters...)
		if err != nil || len(messages) != 0 {
			return messages, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return []Message{}, nil
		case <-changed:
		case <-poll.C:
		}
	}
}
