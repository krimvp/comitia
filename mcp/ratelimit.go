package mcp

import (
	"sync"
	"time"
)

// maxLimiterKeys bounds how many credentials the limiter tracks at once, so a
// burst of arbitrary credentials cannot grow memory without limit.
const maxLimiterKeys = 4096

// keyLimiter is a per-key token bucket. Over HTTP there is no persistent
// connection to attach state to, so the bucket is keyed by credential: one
// misbehaving identity cannot exhaust another's budget.
type keyLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket

	rate  float64 // tokens per second
	burst float64 // bucket capacity
	now   func() time.Time

	calls uint64
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newKeyLimiter(ratePerSecond float64, burst int) *keyLimiter {
	return &keyLimiter{
		buckets: make(map[string]*bucket),
		rate:    ratePerSecond,
		burst:   float64(burst),
		now:     time.Now,
	}
}

// allow reports whether one request may proceed for the given key.
func (l *keyLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.calls++
	// Opportunistic pruning: keeps the bucket map bounded even when an attacker
	// cycles through arbitrary invalid credentials.
	if l.calls%1024 == 0 {
		l.pruneLocked()
	}

	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		// Hard cap on tracked keys. A burst of arbitrary credentials must not
		// grow the map without bound, so above the limit we evict the least
		// recently seen entry. The worst case is that an evicted well-behaved
		// credential gets a fresh burst allowance, which is acceptable for a
		// local backstop; the edge limiter remains the primary control.
		if len(l.buckets) >= maxLimiterKeys {
			l.evictOldestLocked()
		}
		l.buckets[key] = &bucket{tokens: l.burst - 1, last: now}
		return true
	}
	// Refill based on elapsed time, capped at the burst size.
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// evictOldestLocked removes the least recently seen bucket. Callers must hold
// l.mu and ensure the map is non-empty.
func (l *keyLimiter) evictOldestLocked() {
	var oldestKey string
	var oldest time.Time
	first := true
	for k, b := range l.buckets {
		if first || b.last.Before(oldest) {
			oldestKey, oldest, first = k, b.last, false
		}
	}
	delete(l.buckets, oldestKey)
}

// pruneLocked drops buckets that have fully refilled, so an attacker cycling
// through arbitrary invalid credentials cannot grow the map without bound.
// Callers must hold l.mu.
func (l *keyLimiter) pruneLocked() {
	now := l.now()
	for k, b := range l.buckets {
		if b.tokens >= l.burst && now.Sub(b.last) > time.Minute {
			delete(l.buckets, k)
		}
	}
}
