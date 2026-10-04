package comitia

import (
	"context"
	"math"
	"sync"
	"time"
)

const MaxMessageWait = 60 * time.Second

func messageFilters(filters []MessageFilters) (MessageFilters, error) {
	if len(filters) > 1 {
		return MessageFilters{}, Invalidf("at most one filter set allowed")
	}
	var f MessageFilters
	if len(filters) == 1 {
		f = filters[0]
	}
	if f.MaxBytes < 0 || f.MaxBytes > MaxMessageBytes {
		return f, Invalidf("max_bytes must be between 0 and %d", MaxMessageBytes)
	}
	if f.ForInstance != "" && !validInstanceID(f.ForInstance) {
		return f, Invalidf("for_instance must be 1-64 letters, digits, '.', '_' or '-'")
	}
	if f.FromInstance != "" && !validInstanceID(f.FromInstance) {
		return f, Invalidf("from_instance must be 1-64 letters, digits, '.', '_' or '-'")
	}
	if instance := f.agentInstance(); instance != "" && !validInstanceID(instance) {
		return f, Invalidf("agent_instance must be 1-64 letters, digits, '.', '_' or '-'")
	}
	return f, nil
}

func (f MessageFilters) agentInstance() string {
	if f.AgentInstance == nil {
		return ""
	}
	return *f.AgentInstance
}

// MessageWaitTimeout converts wire seconds without overflowing a duration.
func MessageWaitTimeout(seconds float64) (time.Duration, error) {
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0, Invalidf("timeout_seconds must be a finite nonnegative number")
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

// MessageScanStore is an optional ReadStore capability for filtered reads.
// MessagesScanned applies the same filters and limits as Messages. Its watermark
// is at least after; every match in (after, watermark] must be returned.
// Matches and watermark must come from the same View snapshot. Empty reads may
// advance through nonmatches; limited reads must not skip an omitted match.
// The watermark is read progress for these filters, never an acknowledgement.
// Stores without this capability retain Messages fallback behavior.
type MessageScanStore interface {
	MessagesScanned(string, string, int64, int, MessageFilters) ([]Message, int64, error)
}

func (s *Service) scanMessages(ctx context.Context, raw, cid string, after int64, limit int, filters []MessageFilters) ([]Message, int64, error) {
	f, err := messageFilters(filters)
	if err != nil {
		return nil, after, err
	}
	if after < 0 {
		return nil, after, Invalidf("after must be nonnegative")
	}
	if limit < 1 || limit > 1000 {
		return nil, after, Invalidf("limit must be between 1 and 1000")
	}
	instance := f.agentInstance()
	var sid string
	var heartbeat bool
	var out []Message
	scanned := after
	err = s.store.View(ctx, func(r ReadStore) error {
		a, _, e := s.require(r, raw, cid, PermissionRead)
		if e != nil {
			return e
		}
		sid = a.session.ID
		if scan, ok := r.(MessageScanStore); ok {
			out, scanned, e = scan.MessagesScanned(cid, a.session.ID, after, limit, f)
		} else {
			out, e = r.Messages(cid, a.session.ID, after, limit, f)
		}
		if e == nil {
			heartbeat = s.instanceHeartbeatDue(r, sid, cid, instance)
		}
		return e
	})
	// Presence follows a successful read and remains throttled.
	if err == nil && heartbeat {
		s.touchInstance(ctx, sid, cid, &AgentInstance{ID: instance})
	}
	if err == nil {
		var bounded []Message
		bounded, err = boundMessages(out, f.MaxBytes)
		if err == nil && len(bounded) < len(out) {
			scanned = bounded[len(bounded)-1].Sequence
		}
		out = bounded
		if len(out) > 0 {
			scanned = max(scanned, out[len(out)-1].Sequence)
		}
		scanned = max(after, scanned)
	}
	return out, scanned, err
}

// WaitMessages returns up to 100 matches, or an empty slice on timeout. It
// subscribes before reading to avoid losing an append between read and wait.
// Each read rechecks authorization; no transaction remains open while waiting.
func (s *Service) WaitMessages(ctx context.Context, raw, cid string, after int64, timeout time.Duration, filters ...MessageFilters) ([]Message, error) {
	messages, _, err := s.waitMessagesScanned(ctx, raw, cid, after, timeout, filters...)
	return messages, err
}

func (s *Service) waitMessagesScanned(ctx context.Context, raw, cid string, after int64, timeout time.Duration, filters ...MessageFilters) ([]Message, int64, error) {
	if timeout < 0 {
		return nil, after, Invalidf("timeout must be nonnegative")
	}
	if timeout > MaxMessageWait {
		timeout = MaxMessageWait
	}
	scanCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		scanCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	v, unsubscribe := s.messageNotifications.subscribe(cid)
	defer unsubscribe()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil, after, err
		}
		changed := s.messageNotifications.channel(v)
		messages, scanned, err := s.scanMessages(scanCtx, raw, cid, after, 100, filters)
		if err := ctx.Err(); err != nil {
			return nil, after, err
		}
		if err == nil && len(messages) != 0 {
			return messages, scanned, nil
		}
		if scanCtx.Err() != nil {
			if err == nil {
				after = scanned
			}
			return []Message{}, after, nil
		}
		if err != nil {
			return messages, after, err
		}
		after = scanned
		select {
		case <-ctx.Done():
			return nil, after, ctx.Err()
		case <-timer.C:
			if err := ctx.Err(); err != nil {
				return nil, after, err
			}
			return []Message{}, after, nil
		case <-changed:
		case <-poll.C:
		}
	}
}
