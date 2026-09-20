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
	// AllowedOrigins is an Origin allowlist for DNS-rebinding defence, as
	// required by the MCP transport specification. Requests without an Origin
	// header (non-browser clients such as ChatGPT or curl) are always allowed.
	// An empty list rejects every request that carries an Origin.
	AllowedOrigins []string

	// MaxBody caps the request body in bytes.
	MaxBody int64

	// ReadHeaderTimeout guards slow-header clients.
	ReadHeaderTimeout time.Duration

	// RatePerSecond and RateBurst bound the steady-state and peak request rate
	// per credential. Zero disables limiting. This is a local backstop against
	// accidental agent loops; edge rate limiting remains the primary control.
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
func NewHTTPHandler(api comitia.API, cfg HTTPConfig) http.Handler {
	if cfg.MaxBody <= 0 {
		cfg.MaxBody = 1 << 20 // 1 MiB, matching the REST adapter's cap
	}
	t := &httpTransport{api: api, cfg: cfg}
	if cfg.RatePerSecond > 0 && cfg.RateBurst > 0 {
		t.limiter = newKeyLimiter(cfg.RatePerSecond, cfg.RateBurst)
	}
	return t
}

type httpTransport struct {
	api     comitia.API
	cfg     HTTPConfig
	limiter *keyLimiter
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

	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPCError(w, http.StatusBadRequest, -32700, "parse error")
		return
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		writeRPCError(w, http.StatusBadRequest, -32600, "invalid request")
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

	if t.limiter != nil && !t.limiter.allow(cred) {
		w.Header().Set("Retry-After", "1")
		writeRPCError(w, http.StatusTooManyRequests, -32000, "rate limit exceeded")
		return
	}

	// A notification carries no id and is acknowledged without a response body.
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// The stdio server enforces an initialize handshake because a single
	// connection is stateful. Over HTTP each request is independent, so the
	// per-connection gate does not apply; the credential is the real
	// authorization boundary and is checked on every call.
	srv := &Server{API: t.api, Credential: cred}
	res := srv.handle(r.Context(), req)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(res)
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
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}
