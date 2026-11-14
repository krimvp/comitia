package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

// stubAPI records calls and returns canned values, so the transport can be
// tested without a database.
type stubAPI struct {
	comitia.API
	mu      sync.Mutex
	cred    string
	calls   int
	lastCtx string
}

func (s *stubAPI) ListParticipants(ctx context.Context, credential, contextID string, limit int) ([]comitia.Participant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.cred = credential
	s.lastCtx = contextID
	if credential != "good" {
		return nil, comitia.ErrUnauthenticated
	}
	return []comitia.Participant{{SessionID: "abc", Name: "peer"}}, nil
}

func post(t *testing.T, h http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	return postPath(t, h, PathPrefix, body, headers)
}

func postPath(t *testing.T, h http.Handler, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// toolError reports whether the response carries the tool-level error signal.
// Authentication failures on this transport are deliberately 200 + isError,
// not an HTTP status, so tests must assert on the flag rather than the text.
func toolError(t *testing.T, body string) bool {
	t.Helper()
	var resp struct {
		Result struct {
			IsError *bool `json:"isError"`
		} `json:"result"`
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		return false
	}
	if resp.Error != nil {
		return true
	}
	return resp.Result.IsError != nil && *resp.Result.IsError
}

func call(name string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name +
		`","arguments":{"context_id":"ctx1"}}}`
}

func TestHTTPRequiresCredential(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	rec := post(t, h, call("list_participants"), nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Fatal("missing WWW-Authenticate challenge")
	} else if strings.Contains(got, "resource_metadata") {
		t.Fatalf("challenge must not advertise OAuth metadata: %q", got)
	}
}

func TestHTTPToolCallUsesHeaderCredential(t *testing.T) {
	api := &stubAPI{}
	h := NewHTTPHandler(api, HTTPConfig{})
	rec := post(t, h, call("list_participants"),
		map[string]string{"Authorization": "Bearer good"})
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Result.IsError {
		t.Fatalf("tool call reported error: %s", rec.Body.String())
	}
	if api.cred != "good" || api.lastCtx != "ctx1" {
		t.Fatalf("stub got cred=%q ctx=%q", api.cred, api.lastCtx)
	}
}

func TestHTTPBadCredentialIsToolError(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	rec := post(t, h, call("list_participants"),
		map[string]string{"Authorization": "Bearer wrong"})
	// The transport authenticated the request shape; Comitia rejected the
	// credential, which surfaces as a tool-level error rather than a transport 401.
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"isError":true`) {
		t.Fatalf("want isError true, got %s", rec.Body.String())
	}
}

func TestHTTPNotificationAccepted(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	rec := post(t, h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		map[string]string{"Authorization": "Bearer good"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("notification must have empty body, got %q", rec.Body.String())
	}
}

func TestHTTPInitializeWorksWithoutHandshake(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		map[string]string{"Authorization": "Bearer good"})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "comitia") {
		t.Fatalf("want successful initialize, got %d %s", rec.Code, rec.Body.String())
	}
	// Tools must also work on a fresh request: no per-connection state.
	rec = post(t, h, call("list_participants"),
		map[string]string{"Authorization": "Bearer good"})
	if rec.Code != http.StatusOK {
		t.Fatalf("stateless tools/call failed: %d", rec.Code)
	}
}

func TestHTTPProtocolVersionRejected(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	rec := post(t, h, call("list_participants"), map[string]string{
		"Authorization":        "Bearer good",
		"MCP-Protocol-Version": "1999-01-01",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for bad protocol version, got %d", rec.Code)
	}
	// The rejection must advertise what is supported, using the standard
	// UNSUPPORTED_PROTOCOL_VERSION code, so clients can fall back.
	var resp struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error.Code != -32022 {
		t.Fatalf("want code -32022, got %d (%s)", resp.Error.Code, rec.Body.String())
	}
	if !strings.Contains(resp.Error.Message, "supported") {
		t.Fatalf("rejection should list supported versions: %q", resp.Error.Message)
	}
}

func TestHTTPSupportedProtocolVersionsAccepted(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	for _, rev := range SupportedProtocolVersions {
		rec := post(t, h, call("list_participants"), map[string]string{
			"Authorization":        "Bearer good",
			"MCP-Protocol-Version": rev,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("revision %s rejected: %d %s", rev, rec.Code, rec.Body.String())
		}
	}
}

// A client that optimistically announces a newer revision during initialize
// must be negotiated down, not refused: refusing deadlocks clients that do not
// retry the handshake. Regression test for the Hermes interop failure, where a
// blanket header rejection broke connection.
func TestHTTPInitializeNegotiatesDownFromUnknownRevision(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	for _, rev := range []string{"2025-11-25", "2026-07-28", "2099-01-01"} {
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"` +
			rev + `","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`
		rec := post(t, h, body, map[string]string{
			"Authorization":        "Bearer good",
			"MCP-Protocol-Version": rev,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("initialize announcing %s was refused: %d %s", rev, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), ProtocolVersion) {
			t.Fatalf("initialize should answer with the supported revision, got %s", rec.Body.String())
		}
	}
}

// The negotiated revision, once announced, must be accepted for later calls.
func TestHTTPSettledRevisionAcceptedAfterNegotiation(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"c","version":"1"}}}`
	post(t, h, init, map[string]string{"Authorization": "Bearer good",
		"MCP-Protocol-Version": "2025-11-25"})

	// Now the client uses the revision the server reported.
	rec := post(t, h, call("list_participants"), map[string]string{
		"Authorization":        "Bearer good",
		"MCP-Protocol-Version": ProtocolVersion,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("settled revision refused: %d %s", rec.Code, rec.Body.String())
	}
}

// ChatGPT's connector UI has no custom-header field, so the credential travels
// in the URL. This must authenticate exactly like the header form.
func TestHTTPCredentialInURLPath(t *testing.T) {
	api := &stubAPI{}
	h := NewHTTPHandler(api, HTTPConfig{})

	rec := postPath(t, h, PathPrefix+"/good", call("list_participants"), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("URL credential rejected: %d %s", rec.Code, rec.Body.String())
	}
	if api.cred != "good" {
		t.Fatalf("credential from URL not used: %q", api.cred)
	}
}

// A bad URL credential must fail exactly like a bad header credential. Note the
// tool error surfaces as a 200 with an error object, which is the established
// behaviour for this transport.
func TestHTTPCredentialInURLRejected(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	rec := postPath(t, h, PathPrefix+"/wrong", call("list_participants"), nil)
	if rec.Code != http.StatusOK || !toolError(t, rec.Body.String()) {
		t.Fatalf("bad URL credential should be a tool error, got %d %s", rec.Code, rec.Body.String())
	}
}

// The header must win, so a URL segment cannot override or downgrade an
// explicitly presented credential.
func TestHTTPHeaderCredentialTakesPrecedenceOverPath(t *testing.T) {
	api := &stubAPI{}
	h := NewHTTPHandler(api, HTTPConfig{})
	rec := postPath(t, h, PathPrefix+"/wrong", call("list_participants"),
		map[string]string{"Authorization": "Bearer good"})
	if rec.Code != http.StatusOK {
		t.Fatalf("header should authenticate: %d %s", rec.Code, rec.Body.String())
	}
	if api.cred != "good" {
		t.Fatalf("header credential should win, got %q", api.cred)
	}
}

// Deeper paths must not be treated as credentials, so nothing URL-shaped can
// smuggle extra path structure past the router.
func TestHTTPCredentialPathMustBeSingleSegment(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	for _, p := range []string{PathPrefix + "/a/b", PathPrefix + "/", PathPrefix} {
		rec := postPath(t, h, p, call("list_participants"), nil)
		if rec.Code < 400 && !toolError(t, rec.Body.String()) {
			t.Fatalf("path %q should not authenticate: %d %s", p, rec.Code, rec.Body.String())
		}
	}
}

// Every other route stays unreachable, including sibling paths of the prefix.
func TestHTTPSiblingPathsRejected(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	for _, p := range []string{"/ui/", "/v1/participants", "/mcp-other/good"} {
		rec := postPath(t, h, p, call("list_participants"), nil)
		if rec.Code < 400 && !toolError(t, rec.Body.String()) {
			t.Fatalf("sibling path %q unexpectedly served: %d %s", p, rec.Code, rec.Body.String())
		}
	}
}

// server/discover must exist for modern-era clients, and must advertise the
// revision this server actually implements.
func TestHTTPServerDiscover(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{}}`,
		map[string]string{"Authorization": "Bearer good"})
	if rec.Code != http.StatusOK {
		t.Fatalf("server/discover failed: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Result struct {
			SupportedVersions []string       `json:"supportedVersions"`
			Capabilities      map[string]any `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Result.SupportedVersions) == 0 {
		t.Fatalf("discover must list supported versions: %s", rec.Body.String())
	}
	for _, v := range resp.Result.SupportedVersions {
		if !SupportedProtocolVersion(v) {
			t.Fatalf("discover advertises %q which the server does not accept", v)
		}
	}
	if _, ok := resp.Result.Capabilities["tools"]; !ok {
		t.Fatalf("discover must advertise tools capability: %s", rec.Body.String())
	}
	// Advertising capabilities the server does not implement would invite
	// clients to call methods that 404.
	for _, unwanted := range []string{"resources", "prompts", "completions", "logging"} {
		if _, ok := resp.Result.Capabilities[unwanted]; ok {
			t.Fatalf("discover advertises unimplemented capability %q", unwanted)
		}
	}
}

func TestHTTPOriginAllowlist(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{AllowedOrigins: []string{"https://ok.example"}})

	// No Origin header: allowed (non-browser client).
	if rec := post(t, h, call("list_participants"),
		map[string]string{"Authorization": "Bearer good"}); rec.Code != http.StatusOK {
		t.Fatalf("origin-less request rejected: %d", rec.Code)
	}
	// Allowed origin.
	if rec := post(t, h, call("list_participants"), map[string]string{
		"Authorization": "Bearer good", "Origin": "https://ok.example"}); rec.Code != http.StatusOK {
		t.Fatalf("allowlisted origin rejected: %d", rec.Code)
	}
	// Disallowed origin: rejected before auth is even considered.
	if rec := post(t, h, call("list_participants"), map[string]string{
		"Authorization": "Bearer good", "Origin": "https://evil.example"}); rec.Code != http.StatusForbidden {
		t.Fatalf("want 403 for bad origin, got %d", rec.Code)
	}
}

func TestHTTPBodyLimit(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{MaxBody: 64})
	big := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_participants","arguments":{"context_id":"` +
		strings.Repeat("x", 200) + `"}}}`
	rec := post(t, h, big, map[string]string{"Authorization": "Bearer good"})
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", rec.Code)
	}
}

func TestHTTPRejectsInvalidMessages(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	auth := map[string]string{"Authorization": "Bearer good"}
	for _, tc := range []struct {
		body string
		code int
	}{
		{`{"jsonrpc":"2.0","id":null,"method":"ping"}`, -32600},
		{`{"jsonrpc":"2.0","id":{"a":1},"method":"ping"}`, -32600},
		{`{"jsonrpc":"2.0","id":[1],"method":"ping"}`, -32600},
		{`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`, -32600},
		{`{"jsonrpc":"2.0","id":`, -32700},
	} {
		rec := post(t, h, tc.body, auth)
		var res stdioResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || rec.Code != http.StatusBadRequest || string(res.ID) != "null" || res.Error == nil || res.Error.Code != tc.code {
			t.Fatalf("%s: status %d body %s", tc.body, rec.Code, rec.Body.String())
		}
	}
}

func TestHTTPClientResponseAccepted(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	rec := post(t, h, `{"jsonrpc":"2.0","id":3,"result":{}}`, map[string]string{"Authorization": "Bearer good"})
	if rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Fatalf("client response: status %d body %q", rec.Code, rec.Body.String())
	}
}

func TestHTTPUnknownToolIsInvalidParams(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	rec := post(t, h, call("no_such_tool"), map[string]string{"Authorization": "Bearer good"})
	var res stdioResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || string(res.ID) != "1" || res.Error == nil || res.Error.Code != -32602 || len(res.Result) != 0 {
		t.Fatalf("unknown tool: status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPDecodeErrorsNameFieldsAndJSONTypes(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	for args, want := range map[string]string{
		`{"context_id":"ctx1","strict_recipients":"yes"}`: "strict_recipients must have type boolean",
		`{"context_id":"ctx1","limit":"10"}`:              "limit must have type integer",
		`{"context_id":"ctx1","schemas":"a"}`:             "schemas must have type array",
	} {
		rec := post(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"replay_messages","arguments":`+args+`}}`, map[string]string{"Authorization": "Bearer good"})
		if body := rec.Body.String(); !strings.Contains(body, want) || strings.Contains(body, "MessageFilters") || strings.Contains(body, "bool ") || strings.Contains(body, "int ") {
			t.Fatalf("%s: want %q, got %s", args, want, body)
		}
	}
}

func TestHTTPGetNotAllowed(t *testing.T) {
	h := NewHTTPHandler(&stubAPI{}, HTTPConfig{})
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", rec.Code)
	}
	if allow := rec.Header().Get("Allow"); allow != "POST" {
		t.Fatalf("want Allow: POST, got %q", allow)
	}
}

func TestHTTPRateLimitPerCredential(t *testing.T) {
	api := &stubAPI{}
	h := NewHTTPHandler(api, HTTPConfig{RatePerSecond: 1, RateBurst: 3})

	limited := false
	for i := 0; i < 10; i++ {
		rec := post(t, h, call("list_participants"),
			map[string]string{"Authorization": "Bearer good"})
		if rec.Code == http.StatusTooManyRequests {
			limited = true
			if rec.Header().Get("Retry-After") == "" {
				t.Fatal("429 without Retry-After")
			}
			break
		}
	}
	if !limited {
		t.Fatal("expected rate limiting to trigger")
	}

	// A different credential has its own bucket and must still work.
	rec := post(t, h, call("list_participants"),
		map[string]string{"Authorization": "Bearer other"})
	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("second credential was throttled by the first credential's bucket")
	}
}

func TestKeyLimiterBounded(t *testing.T) {
	l := newKeyLimiter(1, 1)
	now := time.Now()
	l.now = func() time.Time { return now }

	// Push far more distinct credentials than the cap allows. Freezing the clock
	// means time-based pruning cannot help, so only the hard cap bounds memory.
	for i := 0; i < maxLimiterKeys*3; i++ {
		l.allow("cred-" + strconv.Itoa(i))
	}

	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n > maxLimiterKeys {
		t.Fatalf("bucket map exceeded cap: %d entries (cap %d)", n, maxLimiterKeys)
	}
	t.Logf("tracked %d buckets for %d distinct credentials (cap %d)",
		n, maxLimiterKeys*3, maxLimiterKeys)

	// A well-behaved credential still works after all that churn.
	if !l.allow("cred-1") {
		t.Log("note: evicted credential received a fresh burst, which is expected")
	}

	// Time-based pruning reclaims fully refilled buckets.
	now = now.Add(2 * time.Minute)
	for i := 0; i < maxLimiterKeys; i++ {
		l.allow("more-" + strconv.Itoa(i))
	}
	l.mu.Lock()
	after := len(l.buckets)
	l.mu.Unlock()
	if after > maxLimiterKeys {
		t.Fatalf("cap not enforced after pruning: %d", after)
	}
}
