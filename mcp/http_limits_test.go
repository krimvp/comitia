package mcp

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

func TestHTTPRotatingCredentialsAreBoundedBeforeReachingTheDaemon(t *testing.T) {
	api := &stubAPI{}
	h := NewHTTPHandler(api, HTTPConfig{RatePerSecond: 1, RateBurst: 3})
	good := map[string]string{"Authorization": "Bearer good"}
	if rec := post(t, h, call("list_participants"), good); rec.Code != http.StatusOK || toolError(t, rec.Body.String()) {
		t.Fatalf("valid credential: %d %s", rec.Code, rec.Body.String())
	}
	limited := 0
	for i := 0; i < 50; i++ {
		rec := postPath(t, h, PathPrefix+"/bogus"+strconv.Itoa(i), call("list_participants"), nil)
		if rec.Code == http.StatusTooManyRequests {
			limited++
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("429 without Retry-After")
			}
		}
	}
	api.mu.Lock()
	forwarded := api.calls - 1
	api.mu.Unlock()
	// The failure budget is the burst plus at most a token or two of refill.
	if limited == 0 || forwarded > 5 {
		t.Fatalf("rotating credentials: %d limited, %d forwarded", limited, forwarded)
	}
	// A credential already shown valid is not locked out by the flood.
	if rec := post(t, h, call("list_participants"), good); rec.Code != http.StatusOK || toolError(t, rec.Body.String()) {
		t.Fatalf("valid credential after flood: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPUnverifiedCredentialsCannotEvictValidBuckets(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{RatePerSecond: 1, RateBurst: 3}).(*httpTransport)
	now := time.Now()
	h.limiter.now = func() time.Time { return now }
	h.unverified.now = h.limiter.now
	good := map[string]string{"Authorization": "Bearer good"}
	throttled := false
	for i := 0; i < 10 && !throttled; i++ {
		throttled = post(t, h, call("list_participants"), good).Code == http.StatusTooManyRequests
	}
	if !throttled {
		t.Fatal("valid credential was never throttled")
	}
	initialize := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` + ProtocolVersion + `"}}`
	for i := 0; i <= maxLimiterKeys; i++ {
		post(t, h, initialize, map[string]string{"Authorization": "Bearer junk" + strconv.Itoa(i)})
	}
	if rec := post(t, h, call("list_participants"), good); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("junk credentials reset a valid credential's bucket: %d", rec.Code)
	}
}

func TestUnverifiedGateForgetsRevokedCredentials(t *testing.T) {
	g := newUnverifiedGate(1, 2)
	g.settle("c", false, true, true)
	if !g.isVerified("c") {
		t.Fatal("authenticated credential not verified")
	}
	g.settle("c", false, true, false)
	if g.isVerified("c") {
		t.Fatal("rejected credential stayed verified")
	}
	now := time.Now()
	g.now = func() time.Time { return now }
	g.settle("d", false, true, true)
	now = now.Add(verifiedTTL)
	if g.isVerified("d") {
		t.Fatal("verification outlived its TTL")
	}
}

type blockingWaitAPI struct {
	stubAPI
	started chan struct{}
	release chan struct{}
}

func (s *blockingWaitAPI) WaitMessages(ctx context.Context, credential, contextID string, after int64, timeout time.Duration, filters ...comitia.MessageFilters) ([]comitia.Message, error) {
	s.started <- struct{}{}
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	return nil, nil
}

func TestHTTPCapsConcurrentWaitsPerCredential(t *testing.T) {
	api := &blockingWaitAPI{started: make(chan struct{}, 64), release: make(chan struct{})}
	h := NewHTTPHandler(api, HTTPConfig{})
	wait := func(cred string) int {
		return post(t, h, call("wait_messages"), map[string]string{"Authorization": "Bearer " + cred}).Code
	}
	var wg sync.WaitGroup
	codes := make(chan int, maxHTTPWaitsPerCredential+1)
	for i := 0; i < maxHTTPWaitsPerCredential; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- wait("good") }()
	}
	for i := 0; i < maxHTTPWaitsPerCredential; i++ {
		select {
		case <-api.started:
		case <-time.After(10 * time.Second):
			t.Fatal("waits did not start")
		}
	}
	rec := post(t, h, call("wait_messages"), map[string]string{"Authorization": "Bearer good"})
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "wait_messages calls in flight") {
		t.Fatalf("over-cap wait: %d %q %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
	// Other credentials and other tools are unaffected.
	go func() { codes <- wait("other") }()
	select {
	case <-api.started:
	case <-time.After(10 * time.Second):
		t.Fatal("another credential's wait was refused")
	}
	if rec := post(t, h, call("list_participants"), map[string]string{"Authorization": "Bearer good"}); rec.Code != http.StatusOK {
		t.Fatalf("non-wait call: %d", rec.Code)
	}
	close(api.release)
	wg.Wait()
	for i := 0; i < maxHTTPWaitsPerCredential+1; i++ {
		if code := <-codes; code != http.StatusOK {
			t.Fatalf("wait returned %d", code)
		}
	}
	// Slots are returned once calls finish.
	go func() { <-api.started }()
	if code := wait("good"); code != http.StatusOK {
		t.Fatalf("wait after release: %d", code)
	}
}

type blockingAuthAPI struct {
	stubAPI
	started chan struct{}
	release chan struct{}
}

func (s *blockingAuthAPI) ListParticipants(ctx context.Context, credential, contextID string, limit int) ([]comitia.Participant, error) {
	s.started <- struct{}{}
	<-s.release
	return s.stubAPI.ListParticipants(ctx, credential, contextID, limit)
}

func TestHTTPConcurrentUnverifiedCallsShareOneBudget(t *testing.T) {
	const burst = 3
	api := &blockingAuthAPI{started: make(chan struct{}, 64), release: make(chan struct{})}
	h := NewHTTPHandler(api, HTTPConfig{RatePerSecond: 0.001, RateBurst: burst})
	var wg sync.WaitGroup
	for i := 0; i < burst; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			postPath(t, h, PathPrefix+"/slow"+strconv.Itoa(i), call("list_participants"), nil)
		}(i)
	}
	for i := 0; i < burst; i++ {
		select {
		case <-api.started:
		case <-time.After(10 * time.Second):
			t.Fatal("unverified calls did not start")
		}
	}
	// While those are still in flight, further unverified credentials are
	// refused before reaching the daemon.
	rec := postPath(t, h, PathPrefix+"/another", call("list_participants"), nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("concurrent unverified call: %d %s", rec.Code, rec.Body.String())
	}
	close(api.release)
	wg.Wait()
	// A valid credential's tokens are refunded, so it never drains the budget.
	h = NewHTTPHandler(&stubAPI{}, HTTPConfig{RatePerSecond: 0.001, RateBurst: burst})
	tr := h.(*httpTransport)
	for i := 0; i < burst+2; i++ {
		tr.gate.verified, tr.unverified = map[string]time.Time{}, newKeyLimiter(0.001, burst)
		if rec := post(t, h, call("list_participants"), map[string]string{"Authorization": "Bearer good"}); rec.Code != http.StatusOK || toolError(t, rec.Body.String()) {
			t.Fatalf("valid unverified call %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
}
