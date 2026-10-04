// Package httpapi exposes comitia.API over a small, strict JSON protocol.
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/krimvp/comitia"
)

const maxRequestBody = 1 << 20
const maxResponseBody = 16 << 20

var ErrResponseTooLarge = errors.New("remote response exceeds 16777216-byte limit; retry message reads with a smaller max_bytes or limit")

var ErrBootstrapOffline = errors.New("bootstrap is available only to embedded clients")

type handler struct {
	api        comitia.API
	diagnostic comitia.DiagnosticHook
	routes     map[string]endpoint
}

// NewHandler returns an HTTP handler. Bootstrap is deliberately not routed.
func NewHandler(api comitia.API) http.Handler {
	return NewHandlerWithDiagnostics(api, nil)
}

// NewHandlerWithDiagnostics supplies an internal failure hook; nil logs safe metadata.
func NewHandlerWithDiagnostics(api comitia.API, hook comitia.DiagnosticHook) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/ui/", inspectorHandler())
	h := &handler{api: api, diagnostic: hook}
	h.routes = h.endpoints()
	mux.Handle("/", h)
	return mux
}

type errorResponse = comitia.ErrorInfo

type endpoint func(context.Context, string, io.Reader) (any, error)

func typedEndpoint[Q, R any](call func(context.Context, string, Q) (R, error)) endpoint {
	return func(ctx context.Context, token string, body io.Reader) (any, error) {
		var q Q
		if err := decodeRequest(body, &q); err != nil {
			return nil, err
		}
		return call(ctx, token, q)
	}
}

func (h *handler) endpoints() map[string]endpoint {
	routes := make(map[string]endpoint)
	for _, group := range []map[string]endpoint{h.sessionRoutes(), h.contextRoutes(), h.grantRoutes(), h.messageRoutes(), h.taskRoutes(), h.presenceRoutes(), h.infoRoutes()} {
		for path, call := range group {
			routes[path] = call
		}
	}
	return routes
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	requestID := comitia.NewRequestID()
	w.Header().Set("X-Request-ID", requestID)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, comitia.Invalidf("method must be POST"), http.StatusMethodNotAllowed)
		return
	}
	token, ok := bearer(r.Header.Get("Authorization"))
	if !ok {
		writeError(w, comitia.ErrUnauthenticated, 0)
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/json" {
		writeError(w, fmt.Errorf("%w: Content-Type must be application/json", comitia.ErrInvalid), 0)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		writeError(w, comitia.ErrNotFound, http.StatusNotFound)
		return
	}
	route := strings.TrimPrefix(r.URL.Path, "/v1/")
	call, found := h.routes[route]
	if !found {
		writeError(w, comitia.ErrNotFound, http.StatusNotFound)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	out, err := call(r.Context(), token, r.Body)
	if err != nil {
		operation := comitia.SafeOperation(route)
		info := comitia.SafeError(operation, err, requestID)
		w.Header().Set("X-Request-ID", info.RequestID)
		comitia.ReportFailure(h.diagnostic, operation, err, info.RequestID, time.Since(started))
		writeOperationError(w, err, 0, operation, requestID)
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}

func ensureEOF(d *json.Decoder) error {
	var x any
	err := d.Decode(&x)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return errors.New("additional JSON value")
	}
	return err
}
func bearer(v string) (string, bool) {
	p := strings.Split(v, " ")
	returnValue := ""
	if len(p) == 2 && p[0] == "Bearer" && p[1] != "" {
		returnValue = p[1]
	}
	return returnValue, returnValue != ""
}
func writeError(w http.ResponseWriter, err error, status int) {
	writeOperationError(w, err, status, "unknown", w.Header().Get("X-Request-ID"))
}
func writeOperationError(w http.ResponseWriter, err error, status int, operation, requestID string) {
	info := comitia.SafeError(operation, err, requestID)
	defaultStatus := http.StatusInternalServerError
	switch info.Code {
	case "unauthenticated":
		defaultStatus = http.StatusUnauthorized
	case "forbidden":
		defaultStatus = http.StatusForbidden
	case "not_found":
		defaultStatus = http.StatusNotFound
	case "conflict", "stale_claim", "bootstrap_used":
		defaultStatus = http.StatusConflict
	case "invalid":
		defaultStatus = http.StatusBadRequest
	case "deadline_exceeded", "cancelled":
		defaultStatus = http.StatusServiceUnavailable
	}
	if status == 0 {
		status = defaultStatus
	}
	if status == http.StatusMethodNotAllowed {
		info.Code = "method_not_allowed"
	}
	if info.Code == "internal" {
		info.Message = "internal server error"
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(info)
}

// Client is a remote implementation of comitia.API.
type Client struct {
	base             *url.URL
	http             *http.Client
	InstanceDefaults comitia.InstanceDefaults
}

// NewClient validates and captures COMITIA_AGENT_INSTANCE / LABEL defaults.
func NewClient(baseURL string, hc *http.Client) (*Client, error) {
	defaults, err := comitia.InstanceDefaultsFromEnv()
	if err != nil {
		return nil, err
	}
	return NewClientWithInstanceDefaults(baseURL, hc, defaults)
}

// NewClientWithInstanceDefaults uses explicitly configured defaults without
// reading the environment. A zero value preserves session-scoped behavior.
func NewClientWithInstanceDefaults(baseURL string, hc *http.Client, defaults comitia.InstanceDefaults) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, comitia.Invalidf("server_url must be an HTTP(S) URL without userinfo, query or fragment")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 70 * time.Second}
	} else {
		clone := *hc
		hc = &clone
	}
	hc.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: u, http: hc, InstanceDefaults: defaults}, nil
}
func (c *Client) call(ctx context.Context, token, path string, in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if len(b) > maxRequestBody {
		return comitia.Invalidf("request exceeds %d-byte limit", maxRequestBody)
	}
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/" + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBody+1))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if len(b) > maxResponseBody {
		return ErrResponseTooLarge
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if resp.StatusCode/100 != 2 {
		var e errorResponse
		if d.Decode(&e) != nil || ensureEOF(d) != nil {
			return fmt.Errorf("http status %d: malformed error response", resp.StatusCode)
		}
		return &comitia.OperationalError{Info: e, Cause: decodeError(e.Code, e.Message)}
	}
	var raw json.RawMessage
	if err = d.Decode(&raw); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if err = ensureEOF(d); err != nil {
		return fmt.Errorf("decode response: trailing data: %w", err)
	}
	raw, err = normalizeFields(raw, reflect.TypeOf(out).Elem(), true, false)
	if err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if err = json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return validateCollections(out)
}
func decodeError(code, msg string) error {
	var base error
	switch code {
	case "unauthenticated":
		base = comitia.ErrUnauthenticated
	case "forbidden":
		base = comitia.ErrForbidden
	case "not_found":
		base = comitia.ErrNotFound
	case "conflict":
		base = comitia.ErrConflict
	case "invalid":
		base = comitia.ErrInvalid
	case "stale_claim":
		base = comitia.ErrStaleClaim
	case "bootstrap_used":
		base = comitia.ErrBootstrapUsed
	case "deadline_exceeded":
		base = context.DeadlineExceeded
	case "cancelled":
		base = context.Canceled
	default:
		return fmt.Errorf("remote error: %s", msg)
	}
	detail := strings.TrimPrefix(msg, base.Error()+": ")
	if detail == base.Error() {
		return base
	}
	return fmt.Errorf("%w: %s", base, detail)
}

var _ comitia.API = (*Client)(nil)
