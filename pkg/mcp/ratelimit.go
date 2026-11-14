// Rate limiting for the HTTP transport: per-credential token buckets, the
// unverified-credential gate and the per-credential long-poll cap.

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

// unverifiedGate bounds requests whose credential has not been shown valid.
// Per-credential buckets cannot: a caller that changes the string on every
// request gets a fresh bucket each time, and behind the loopback proxy every
// caller shares one remote address. Credentials that recently produced an
// authenticated result skip the gate and keep separate buckets, so arbitrary
// strings can neither evict their buckets nor lock them out. Every other tool
// call takes a token from one global budget before it reaches the daemon and
// gets it back only if the daemon authenticates the credential, so concurrent
// floods are bounded too; while the budget is empty such calls are refused.
type unverifiedGate struct {
	mu       sync.Mutex
	verified map[string]time.Time
	budget   bucket
	rate     float64
	burst    float64
	now      func() time.Time
}

// verifiedTTL is how long one authenticated result exempts a credential.
const verifiedTTL = 10 * time.Minute

func newUnverifiedGate(ratePerSecond float64, burst int) *unverifiedGate {
	g := &unverifiedGate{verified: make(map[string]time.Time), rate: ratePerSecond, burst: float64(burst), now: time.Now}
	g.budget = bucket{tokens: g.burst, last: g.now()}
	return g
}

func (g *unverifiedGate) refillLocked(now time.Time) {
	g.budget.tokens += now.Sub(g.budget.last).Seconds() * g.rate
	if g.budget.tokens > g.burst {
		g.budget.tokens = g.burst
	}
	g.budget.last = now
}

func (g *unverifiedGate) isVerified(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	seen, ok := g.verified[key]
	return ok && g.now().Sub(seen) < verifiedTTL
}

// take reserves a token for an unverified tool call; settle returns it.
func (g *unverifiedGate) take() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refillLocked(g.now())
	if g.budget.tokens < 1 {
		return false
	}
	g.budget.tokens--
	return true
}

// settle records a call's outcome. An authenticated credential becomes
// verified and, if reserved, its token is refunded; a rejected one loses any
// verification. Calls whose outcome proves nothing keep their token spent.
func (g *unverifiedGate) settle(key string, reserved, checked, authenticated bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !checked {
		return
	}
	if !authenticated {
		delete(g.verified, key)
		return
	}
	now := g.now()
	if reserved {
		g.refillLocked(now)
		if g.budget.tokens++; g.budget.tokens > g.burst {
			g.budget.tokens = g.burst
		}
	}
	if _, ok := g.verified[key]; !ok && len(g.verified) >= maxLimiterKeys {
		for k, seen := range g.verified {
			if now.Sub(seen) >= verifiedTTL {
				delete(g.verified, k)
			}
		}
		if len(g.verified) >= maxLimiterKeys {
			return
		}
	}
	g.verified[key] = now
}

// waitLimiter caps concurrent long polls per credential, as stdio caps a
// connection's in-flight calls. Entries exist only while calls are in flight.
type waitLimiter struct {
	mu       sync.Mutex
	inFlight map[string]int
	max      int
}

func (l *waitLimiter) acquire(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight[key] >= l.max {
		return false
	}
	l.inFlight[key]++
	return true
}

func (l *waitLimiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.inFlight[key]--; l.inFlight[key] <= 0 {
		delete(l.inFlight, key)
	}
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
