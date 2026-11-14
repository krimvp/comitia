package comitia

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

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
	ended    chan struct{}
}

// EndWaits makes current and later message waits return their timeout result
// after one more read, so a stopping server can drain long polls promptly.
func (s *Service) EndWaits() {
	n := &s.messageNotifications
	n.mu.Lock()
	defer n.mu.Unlock()
	if ended := n.endedLocked(); !isClosed(ended) {
		close(ended)
	}
}

func (n *messageNotifications) endedChannel() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.endedLocked()
}

func (n *messageNotifications) endedLocked() chan struct{} {
	if n.ended == nil {
		n.ended = make(chan struct{})
	}
	return n.ended
}

func isClosed(c <-chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
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

func (s *Service) scanMessages(ctx context.Context, raw, cid string, after int64, limit int, filters []MessageFilters) ([]Message, int64, error) {
	f, err := model.CombineMessageFilters(filters)
	if err != nil {
		return nil, after, err
	}
	after, f, err = resolveMessageAfter(ctx, s, raw, cid, after, f)
	if err != nil {
		return nil, after, err
	}
	if after < 0 {
		return nil, after, Invalidf("after must be nonnegative")
	}
	if limit < 1 || limit > 1000 {
		return nil, after, Invalidf("limit must be between 1 and 1000")
	}
	instance := model.FilterAgentInstance(f)
	var sid string
	var heartbeat bool
	var out []Message
	scanned := after
	err = s.store.View(ctx, func(r ReadStore) error {
		a, _, e := s.require(r, raw, cid, PermissionRead)
		if e != nil {
			return e
		}
		if e := validateMessageQuery(r, cid, f); e != nil {
			return e
		}
		sid = a.session.ID
		for {
			var batch []Message
			next := scanned
			if scan, ok := r.(MessageScanStore); ok {
				batch, next, e = scan.MessagesScanned(cid, sid, scanned, limit, f)
			} else {
				batch, e = r.Messages(cid, sid, scanned, limit, f)
			}
			if e != nil {
				break
			}
			for _, m := range batch {
				next = max(next, m.Sequence)
				if model.FilterIncludesRenewals(f) || !renewedMessage(m) {
					out = append(out, m)
				}
			}
			progressed := next > scanned
			scanned = max(scanned, next)
			if len(out) != 0 || len(batch) == 0 || !progressed {
				break
			}
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
	f, err := model.CombineMessageFilters(filters)
	if err != nil {
		return nil, after, err
	}

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
	after, f, err = resolveMessageAfter(scanCtx, s, raw, cid, after, f)
	if err != nil {
		return nil, after, err
	}
	filters = []MessageFilters{f}
	v, unsubscribe := s.messageNotifications.subscribe(cid)
	defer unsubscribe()
	ended := s.messageNotifications.endedChannel()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticks, stop := s.messageWaitTicker()
	defer stop()
	changes := s.changeVersionStore()
	for {
		if err := ctx.Err(); err != nil {
			return nil, after, err
		}
		changed := s.messageNotifications.channel(v)
		// Read the version before the scan so a commit the scan misses
		// changes the next probe.
		var version uint64
		probed := false
		if changes != nil {
			var err error
			version, err = changes.ChangeVersion(scanCtx)
			probed = err == nil
		}
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
		for idle := 0; ; idle++ {
			select {
			case <-ctx.Done():
				return nil, after, ctx.Err()
			case <-timer.C:
				if err := ctx.Err(); err != nil {
					return nil, after, err
				}
				return []Message{}, after, nil
			case <-ended:
				return []Message{}, after, nil
			case <-changed:
			case <-ticks:
				// Unchanged storage skips the scan; authorization can still
				// lapse by time, so rescan every messageWaitReauthorizePolls.
				if probed && idle+1 < messageWaitReauthorizePolls {
					if current, err := changes.ChangeVersion(scanCtx); err == nil && current == version {
						continue
					}
				}
			}
			break
		}
	}
}

// Idle waits probe for commits every messageWaitPoll and rerun the authorized
// scan at least every messageWaitReauthorizePolls polls (5 s).
const (
	messageWaitPoll             = 200 * time.Millisecond
	messageWaitReauthorizePolls = 25
)

func (s *Service) messageWaitTicker() (<-chan time.Time, func()) {
	if s.waitTicker != nil {
		return s.waitTicker()
	}
	t := time.NewTicker(messageWaitPoll)
	return t.C, t.Stop
}

func (s *Service) changeVersionStore() ChangeVersionStore {
	st := s.store
	if o, ok := st.(*observedStore); ok {
		st = o.Store
	}
	changes, _ := st.(ChangeVersionStore)
	return changes
}
