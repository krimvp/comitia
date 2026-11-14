// The Streamable HTTP transport: NewHTTPHandler, origin and protocol-version
// checks, credential extraction, delegation gating and ServeHTTP.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/krimvp/comitia"
)

// PathPrefix is the route this transport is mounted at. A credential may be
// supplied after it (POST /mcp/<credential>) for clients whose configuration UI
// has no field for an Authorization header.
const PathPrefix = "/mcp"

// HTTPConfig configures the Streamable HTTP transport.
type HTTPConfig struct {
	DiagnosticHook   comitia.DiagnosticHook
	InstanceDefaults comitia.InstanceDefaults

	SessionsDir string
	ServerURL   string
	// AllowDelegation offers delegate_session. It is off by default: credential
	// files land on this host, where a remote caller cannot read them.
	AllowDelegation bool

	// AllowedOrigins is an Origin allowlist for DNS-rebinding defence, as
	// required by the MCP transport specification. Requests without an Origin
	// header (non-browser clients such as ChatGPT or curl) are always allowed.
	// An empty list rejects every request that carries an Origin.
	AllowedOrigins []string

	// MaxBody caps the request body in bytes.
	MaxBody int64

	// RatePerSecond and RateBurst bound the steady-state and peak request rate
	// per credential, and the same budget bounds rejected credentials across
	// all callers (see unverifiedGate). Zero disables limiting. This is a local
	// backstop against accidental agent loops and credential floods; edge rate
	// limiting remains the primary control.
	RatePerSecond float64
	RateBurst     int
}

// NewHTTPHandler serves the MCP server over the Streamable HTTP transport.
//
// The transport is deliberately stateless: each POST carries exactly one
// JSON-RPC message plus the Comitia credential in the Authorization header.
// No MCP session identifier is issued, so there is no server-side session state
// to expire, and any client can retry a request independently. Comitia persists
// message cursors and task leases in its own store, so correctness does not
// depend on connection affinity.
//
// Set http.Server.ReadHeaderTimeout on the listener that hosts this handler;
// a handler receives a request only after its headers have already been read.
func NewHTTPHandler(api comitia.API, cfg HTTPConfig) http.Handler {
	if cfg.MaxBody <= 0 {
		cfg.MaxBody = 1 << 20 // 1 MiB, matching the REST adapter's cap
	}
	t := &httpTransport{api: api, cfg: cfg, waits: &waitLimiter{inFlight: make(map[string]int), max: maxHTTPWaitsPerCredential}}
	if cfg.RatePerSecond > 0 && cfg.RateBurst > 0 {
		t.limiter = newKeyLimiter(cfg.RatePerSecond, cfg.RateBurst)
		t.unverified = newKeyLimiter(cfg.RatePerSecond, cfg.RateBurst)
		t.gate = newUnverifiedGate(cfg.RatePerSecond, cfg.RateBurst)
	}
	return t
}

// maxHTTPWaitsPerCredential matches the stdio in-flight call cap.
const maxHTTPWaitsPerCredential = maxStdioCalls

type httpTransport struct {
	api     comitia.API
	cfg     HTTPConfig
	limiter *keyLimiter // credentials with a recent authenticated result
	// unverified holds every other credential string, so arbitrary strings
	// cannot evict the buckets of credentials known to be valid.
	unverified *keyLimiter
	gate       *unverifiedGate
	waits      *waitLimiter
}

func (t *httpTransport) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !t.originAllowed(r) {
		writeRPCError(w, http.StatusForbidden, -32600, "origin not allowed")
		return
	}
	switch r.Method {
	case http.MethodPost:
		t.post(w, r)
	case http.MethodGet, http.MethodDelete:
		// This server is stateless and exposes no SSE stream or session
		// termination, so these are explicitly unsupported.
		w.Header().Set("Allow", "POST")
		writeRPCError(w, http.StatusMethodNotAllowed, -32600, "method not allowed")
	default:
		w.Header().Set("Allow", "POST")
		writeRPCError(w, http.StatusMethodNotAllowed, -32600, "method not allowed")
	}
}

func (t *httpTransport) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser client
	}
	for _, allowed := range t.cfg.AllowedOrigins {
		if strings.EqualFold(strings.TrimRight(allowed, "/"), strings.TrimRight(origin, "/")) {
			return true
		}
	}
	return false
}

func (t *httpTransport) post(w http.ResponseWriter, r *http.Request) {
	// Read the body first: the protocol header rule is method-dependent, so the
	// method has to be known before the revision can be judged.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, t.cfg.MaxBody))
	if err != nil {
		writeRPCError(w, http.StatusRequestEntityTooLarge, -32600, "request too large")
		return
	}

	req, reject := readRequest(body)
	if reject != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(reject)
		return
	}

	// Protocol revision handling.
	//
	// `initialize` is the negotiation itself, so an unrecognised revision there
	// is negotiated down rather than refused: the server answers with the
	// revision it implements and the client adopts it. Refusing here would
	// deadlock clients that optimistically announce their newest revision.
	//
	// For every other method the revision is already settled, so an
	// unrecognised value is a real error and is refused with the standard
	// UNSUPPORTED_PROTOCOL_VERSION code plus the list we do support.
	if req.Method != "initialize" {
		if v := r.Header.Get("MCP-Protocol-Version"); v != "" && !SupportedProtocolVersion(v) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(response{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{
				Code: -32022, // UNSUPPORTED_PROTOCOL_VERSION
				Message: "unsupported protocol version: requested " + v +
					", supported " + strings.Join(SupportedProtocolVersions, ", "),
			}})
			return
		}
	}

	cred, ok := bearerCredential(r, PathPrefix)
	if !ok {
		// A plain Bearer challenge is returned deliberately: no
		// resource_metadata is advertised, so a client that supports OAuth is
		// not induced to start an OAuth flow this server cannot serve.
		w.Header().Set("WWW-Authenticate", `Bearer realm="comitia"`)
		writeRPCError(w, http.StatusUnauthorized, -32001, "unauthenticated")
		return
	}

	verified := t.gate != nil && t.gate.isVerified(cred)
	if t.limiter != nil {
		limiter := t.unverified
		if verified {
			limiter = t.limiter
		}
		if !limiter.allow(cred) {
			w.Header().Set("Retry-After", "1")
			writeRPCError(w, http.StatusTooManyRequests, -32000, "rate limit exceeded")
			return
		}
	}

	// A notification carries no id, and a client response no method; both are
	// acknowledged without a response body.
	if len(req.ID) == 0 || req.Method == "" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// The stdio server enforces an initialize handshake because a single
	// connection is stateful. Over HTTP each request is independent, so the
	// per-connection gate does not apply; the credential is the real
	// authorization boundary and is checked on every call.
	tool := toolCallName(req)
	if tool == "wait_messages" {
		if !t.waits.acquire(cred) {
			w.Header().Set("Retry-After", "1")
			writeRPCError(w, http.StatusTooManyRequests, -32000, "too many wait_messages calls in flight for this credential; retry after one returns")
			return
		}
		defer t.waits.release(cred)
	}
	reserved := tool != "" && t.gate != nil && !verified
	if reserved && !t.gate.take() {
		w.Header().Set("Retry-After", "1")
		writeRPCError(w, http.StatusTooManyRequests, -32000, "too many calls with unverified credentials; retry later")
		return
	}
	res, refused := t.withoutDelegation(req)
	if !refused {
		srv := &Server{DiagnosticHook: t.cfg.DiagnosticHook, API: t.api, Credential: cred, SessionsDir: t.cfg.SessionsDir, ServerURL: t.cfg.ServerURL, InstanceDefaults: t.cfg.InstanceDefaults}
		res = srv.handle(r.Context(), req)
		if tool != "" && t.gate != nil {
			checked, authenticated := authOutcome(res)
			t.gate.settle(cred, reserved, checked, authenticated)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
}

func toolCallName(req request) string {
	var p struct {
		Name string `json:"name"`
	}
	if req.Method != "tools/call" || json.Unmarshal(req.Params, &p) != nil {
		return ""
	}
	return p.Name
}

// authOutcome reports whether a tool result shows the daemon's verdict on the
// credential: success and post-authentication refusals prove it valid, while
// errors raised before authentication (such as invalid arguments) prove nothing.
func authOutcome(res response) (checked, authenticated bool) {
	result, ok := res.Result.(map[string]any)
	if res.Error != nil || !ok {
		return false, false
	}
	if result["isError"] != true {
		return true, true
	}
	content, _ := result["structuredContent"].(map[string]any)
	info, ok := content["error"].(comitia.ErrorInfo)
	if !ok {
		return false, false
	}
	switch info.Code {
	case "unauthenticated":
		return true, false
	case "forbidden", "not_found", "conflict", "stale_claim":
		return true, true
	}
	return false, false
}

const delegationDisabled = "delegate_session is disabled on this server; start mcp-http with --allow-delegation"

// withoutDelegation answers tools/list without delegate_session and refuses
// calls to it unless delegation is allowed.
func (t *httpTransport) withoutDelegation(req request) (response, bool) {
	r := response{JSONRPC: "2.0", ID: req.ID}
	if t.cfg.AllowDelegation {
		return r, false
	}
	switch req.Method {
	case "tools/list":
		listed := []map[string]any{}
		for _, tool := range tools() {
			if tool["name"] != "delegate_session" {
				listed = append(listed, tool)
			}
		}
		r.Result = map[string]any{"tools": listed}
		return r, true
	case "tools/call":
		var p struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(req.Params, &p) == nil && p.Name == "delegate_session" {
			r.Result = map[string]any{"content": []any{map[string]any{"type": "text", "text": delegationDisabled}}, "isError": true}
			return r, true
		}
	}
	return r, false
}

// bearerCredential extracts a scoped Comitia credential from the request. The
// credential is never accepted as a tool argument.
// bearerCredential extracts the caller's credential. The Authorization header is
// preferred, but a credential may also arrive as a path suffix, as
// /mcp/<credential>.
//
// The path form exists because ChatGPT's custom-connector UI offers only OAuth
// or "No authentication" and provides no field for a custom header, so a
// credential embedded in the URL is the only way to point it at a token-based
// server. The credential is still a bearer secret and is compared and revoked
// identically; only its transport differs.
//
// It is deliberately the *last* path segment, so a URL-shaped secret cannot
// smuggle additional path structure past the router.
func bearerCredential(r *http.Request, pathPrefix string) (string, bool) {
	if parts := strings.Split(r.Header.Get("Authorization"), " "); len(parts) == 2 &&
		strings.EqualFold(parts[0], "Bearer") && parts[1] != "" {
		return parts[1], true
	}

	rest, ok := strings.CutPrefix(r.URL.Path, pathPrefix)
	if !ok {
		return "", false
	}
	rest = strings.Trim(rest, "/")
	if rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}

func writeRPCError(w http.ResponseWriter, status int, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response{JSONRPC: "2.0", Error: &rpcError{code, msg}})
}

// ServeHTTP listens and serves until the context is cancelled.
func ServeHTTP(ctx context.Context, srv *http.Server) error {
	if srv == nil {
		return errors.New("mcp: server is required")
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		_ = srv.Close()
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}
