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
}

// NewHandler returns an HTTP handler. Bootstrap is deliberately not routed.
func NewHandler(api comitia.API) http.Handler {
	return NewHandlerWithDiagnostics(api, nil)
}

// NewHandlerWithDiagnostics supplies an internal failure hook; nil logs safe metadata.
func NewHandlerWithDiagnostics(api comitia.API, hook comitia.DiagnosticHook) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/ui/", inspectorHandler())
	mux.Handle("/", &handler{api: api, diagnostic: hook})
	return mux
}

type request struct {
	MaxBytes      int                         `json:"max_bytes,omitempty"`
	Page          bool                        `json:"page,omitempty"`
	AgentInstance string                      `json:"agent_instance,omitempty"`
	AfterID       string                      `json:"after_id,omitempty"`
	Name          string                      `json:"name,omitempty"`
	PrincipalID   string                      `json:"principal_id,omitempty"`
	SessionID     string                      `json:"session_id,omitempty"`
	GrantID       string                      `json:"grant_id,omitempty"`
	ContextID     string                      `json:"context_id,omitempty"`
	TaskID        string                      `json:"task_id,omitempty"`
	ClaimToken    string                      `json:"claim_token,omitempty"`
	After         int64                       `json:"after,omitempty"`
	Limit         int                         `json:"limit,omitempty"`
	Status        comitia.TaskStatus          `json:"status,omitempty"`
	LeaseVersion  int64                       `json:"lease_version,omitempty"`
	TTL           time.Duration               `json:"ttl,omitempty"`
	CreateContext *comitia.CreateContextInput `json:"create_context,omitempty"`
	Reference     *comitia.AddReferenceInput  `json:"reference,omitempty"`
	Grant         *comitia.GrantInput         `json:"grant,omitempty"`
	Delegate      *comitia.DelegateInput      `json:"delegate,omitempty"`
	Message       *comitia.MessageInput       `json:"message,omitempty"`
	Task          *comitia.TaskInput          `json:"task,omitempty"`

	ChildContext   *comitia.CreateChildContextInput `json:"child_context,omitempty"`
	ClaimInstance  *comitia.AgentInstance           `json:"claim_instance,omitempty"`
	ForInstance    string                           `json:"for_instance,omitempty"`
	FromInstance   string                           `json:"from_instance,omitempty"`
	Schema         string                           `json:"schema,omitempty"`
	TimeoutSeconds *float64                         `json:"timeout_seconds,omitempty"`
	Instance       *comitia.AgentInstance           `json:"instance,omitempty"`
	Resume         json.RawMessage                  `json:"resume,omitempty"`
}

type response struct {
	Page         *comitia.MessagePage  `json:"page,omitempty"`
	ServerInfo   *comitia.ServerInfo   `json:"server_info,omitempty"`
	Contexts     []comitia.WorkContext `json:"contexts,omitempty"`
	Participants []comitia.Participant `json:"participants,omitempty"`
	Principal    *comitia.Principal    `json:"principal,omitempty"`
	Credential   *comitia.Credential   `json:"credential,omitempty"`
	Workspace    *comitia.Workspace    `json:"workspace,omitempty"`
	Context      *comitia.WorkContext  `json:"context,omitempty"`
	Reference    *comitia.Reference    `json:"reference,omitempty"`
	Grant        *comitia.Grant        `json:"grant,omitempty"`
	Grants       []comitia.Grant       `json:"grants,omitempty"`
	Message      *comitia.Message      `json:"message,omitempty"`
	Messages     []comitia.Message     `json:"messages,omitempty"`
	Cursor       *int64                `json:"cursor,omitempty"`
	Task         *comitia.Task         `json:"task,omitempty"`
	Tasks        []comitia.Task        `json:"tasks,omitempty"`
	Claim        *comitia.TaskClaim    `json:"claim,omitempty"`

	Instances []comitia.InstancePresence `json:"instances,omitempty"`

	StorageStatus *comitia.StorageStatus `json:"storage_status,omitempty"`
}

type errorResponse = comitia.ErrorInfo

// routeGroups dispatches /v1/ routes by domain; a group reports found=false for routes it does not serve.
var routeGroups = []func(*handler, context.Context, string, string, request, *response) (bool, error){
	(*handler).routeServerInfo,
	(*handler).routeSessions,
	(*handler).routeContexts,
	(*handler).routeGrants,
	(*handler).routeMessages,
	(*handler).routeTasks,
	(*handler).routePresence,
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
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	var q request
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&q); err != nil {
		writeError(w, comitia.InvalidJSON(err, "request"), 0)
		return
	}
	if err := ensureEOF(d); err != nil {
		writeError(w, fmt.Errorf("%w: multiple JSON values", comitia.ErrInvalid), 0)
		return
	}
	ctx := r.Context()
	var out response
	var err error
	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		writeError(w, comitia.ErrNotFound, http.StatusNotFound)
		return
	}
	route := strings.TrimPrefix(r.URL.Path, "/v1/")
	found := false
	for _, group := range routeGroups {
		if found, err = group(h, ctx, token, route, q, &out); found {
			break
		}
	}
	if !found {
		writeError(w, comitia.ErrNotFound, http.StatusNotFound)
		return
	}
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
func (c *Client) call(ctx context.Context, token, path string, in request, out *response) error {
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
	if err = d.Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if err = ensureEOF(d); err != nil {
		return fmt.Errorf("decode response: trailing data: %w", err)
	}
	return nil
}
func missing(err error) error {
	if err != nil {
		return err
	}
	return errors.New("malformed remote response: missing result")
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
