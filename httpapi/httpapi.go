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

const maxBody = 1 << 20

var ErrBootstrapOffline = errors.New("bootstrap is available only to embedded clients")

type handler struct{ api comitia.API }

// NewHandler returns an HTTP handler. Bootstrap is deliberately not routed.
func NewHandler(api comitia.API) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/ui/", inspectorHandler())
	mux.Handle("/", &handler{api: api})
	return mux
}

type request struct {
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
}

type response struct {
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
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, comitia.ErrInvalid, http.StatusMethodNotAllowed)
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
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	var q request
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(&q); err != nil {
		writeError(w, fmt.Errorf("%w: malformed JSON", comitia.ErrInvalid), 0)
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
	switch strings.TrimPrefix(r.URL.Path, "/v1/") {
	case "contexts/list":
		out.Contexts, err = h.api.ListContexts(ctx, token, q.AfterID, q.Limit)
	case "participants/list":
		out.Participants, err = h.api.ListParticipants(ctx, token, q.ContextID, q.Limit)
	case "principals/create":
		var v comitia.Principal
		v, err = h.api.CreatePrincipal(ctx, token, q.Name)
		out.Principal = &v
	case "sessions/mint":
		var v comitia.Credential
		v, err = h.api.MintSession(ctx, token, q.PrincipalID, q.TTL)
		out.Credential = &v
	case "sessions/delegate":
		if q.Delegate == nil {
			err = comitia.ErrInvalid
			break
		}
		var p comitia.Principal
		var c comitia.Credential
		var g []comitia.Grant
		p, c, g, err = h.api.DelegateSession(ctx, token, *q.Delegate)
		out.Principal = &p
		out.Credential = &c
		out.Grants = g
	case "sessions/revoke":
		err = h.api.RevokeSession(ctx, token, q.SessionID)
	case "grants/revoke":
		err = h.api.RevokeGrant(ctx, token, q.GrantID)
	case "workspaces/create":
		var v comitia.Workspace
		v, err = h.api.CreateWorkspace(ctx, token, q.Name)
		out.Workspace = &v
	case "contexts/create":
		if q.CreateContext == nil {
			err = comitia.ErrInvalid
			break
		}
		var v comitia.WorkContext
		v, err = h.api.CreateContext(ctx, token, *q.CreateContext)
		out.Context = &v
	case "contexts/get":
		var v comitia.WorkContext
		v, err = h.api.GetContext(ctx, token, q.ContextID)
		out.Context = &v
	case "references/add":
		if q.Reference == nil {
			err = comitia.ErrInvalid
			break
		}
		var v comitia.Reference
		v, err = h.api.AddReference(ctx, token, *q.Reference)
		out.Reference = &v
	case "grants/create":
		if q.Grant == nil {
			err = comitia.ErrInvalid
			break
		}
		var v comitia.Grant
		v, err = h.api.GrantContext(ctx, token, *q.Grant)
		out.Grant = &v
	case "messages/append":
		if q.Message == nil {
			err = comitia.ErrInvalid
			break
		}
		var v comitia.Message
		v, err = h.api.AppendMessage(ctx, token, q.ContextID, *q.Message)
		out.Message = &v
	case "messages/replay":
		out.Messages, err = h.api.ReplayMessages(ctx, token, q.ContextID, q.After, q.Limit)
	case "messages/ack":
		err = h.api.AckMessages(ctx, token, q.ContextID, q.After)
	case "messages/cursor":
		var v int64
		v, err = h.api.MessageCursor(ctx, token, q.ContextID)
		out.Cursor = &v
	case "tasks/create":
		if q.Task == nil {
			err = comitia.ErrInvalid
			break
		}
		var v comitia.Task
		v, err = h.api.CreateTask(ctx, token, q.ContextID, *q.Task)
		out.Task = &v
	case "tasks/get":
		var v comitia.Task
		v, err = h.api.GetTask(ctx, token, q.TaskID)
		out.Task = &v
	case "tasks/list":
		out.Tasks, err = h.api.ListTasks(ctx, token, q.ContextID, q.Status, q.Limit)
	case "tasks/claim":
		var v comitia.TaskClaim
		v, err = h.api.ClaimTask(ctx, token, q.TaskID, q.TTL)
		out.Claim = &v
	case "tasks/renew":
		var v comitia.TaskClaim
		v, err = h.api.RenewClaim(ctx, token, q.TaskID, q.ClaimToken, q.LeaseVersion, q.TTL)
		out.Claim = &v
	case "tasks/complete":
		var v comitia.Task
		v, err = h.api.CompleteTask(ctx, token, q.TaskID, q.ClaimToken, q.LeaseVersion)
		out.Task = &v
	default:
		writeError(w, comitia.ErrNotFound, http.StatusNotFound)
		return
	}
	if err != nil {
		writeError(w, err, 0)
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
	code := "internal"
	message := err.Error()
	if status == 0 {
		switch {
		case errors.Is(err, comitia.ErrUnauthenticated):
			status = http.StatusUnauthorized
			code = "unauthenticated"
		case errors.Is(err, comitia.ErrForbidden):
			status = http.StatusForbidden
			code = "forbidden"
		case errors.Is(err, comitia.ErrNotFound):
			status = http.StatusNotFound
			code = "not_found"
		case errors.Is(err, comitia.ErrConflict):
			status = http.StatusConflict
			code = "conflict"
		case errors.Is(err, comitia.ErrStaleClaim):
			status = http.StatusConflict
			code = "stale_claim"
		case errors.Is(err, comitia.ErrBootstrapUsed):
			status = http.StatusConflict
			code = "bootstrap_used"
		case errors.Is(err, comitia.ErrInvalid):
			status = http.StatusBadRequest
			code = "invalid"
		default:
			status = http.StatusInternalServerError
			message = "internal server error"
		}
	} else if status == http.StatusMethodNotAllowed {
		code = "method_not_allowed"
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(errorResponse{code, message})
}

// Client is a remote implementation of comitia.API.
type Client struct {
	base *url.URL
	http *http.Client
}

func NewClient(baseURL string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("invalid server URL")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	} else {
		clone := *hc
		hc = &clone
	}
	hc.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{u, hc}, nil
}
func (c *Client) Bootstrap(context.Context, comitia.BootstrapInput) (comitia.Principal, comitia.Credential, error) {
	return comitia.Principal{}, comitia.Credential{}, ErrBootstrapOffline
}
func (c *Client) call(ctx context.Context, token, path string, in request, out *response) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
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
	lr := io.LimitReader(resp.Body, maxBody+1)
	if resp.StatusCode/100 != 2 {
		var e errorResponse
		if json.NewDecoder(lr).Decode(&e) != nil {
			return fmt.Errorf("http status %d", resp.StatusCode)
		}
		return decodeError(e.Code, e.Message)
	}
	if err = json.NewDecoder(lr).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
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
	default:
		return fmt.Errorf("remote error: %s", msg)
	}
	return fmt.Errorf("%w: %s", base, msg)
}

func (c *Client) CreatePrincipal(x context.Context, t, n string) (comitia.Principal, error) {
	var o response
	e := c.call(x, t, "principals/create", request{Name: n}, &o)
	if o.Principal == nil {
		return comitia.Principal{}, missing(e)
	}
	return *o.Principal, e
}
func (c *Client) MintSession(x context.Context, t, p string, d time.Duration) (comitia.Credential, error) {
	var o response
	e := c.call(x, t, "sessions/mint", request{PrincipalID: p, TTL: d}, &o)
	if o.Credential == nil {
		return comitia.Credential{}, missing(e)
	}
	return *o.Credential, e
}
func (c *Client) DelegateSession(x context.Context, t string, i comitia.DelegateInput) (comitia.Principal, comitia.Credential, []comitia.Grant, error) {
	var o response
	e := c.call(x, t, "sessions/delegate", request{Delegate: &i}, &o)
	if o.Principal == nil || o.Credential == nil {
		return comitia.Principal{}, comitia.Credential{}, nil, missing(e)
	}
	return *o.Principal, *o.Credential, o.Grants, e
}
func (c *Client) RevokeSession(x context.Context, t, id string) error {
	return c.call(x, t, "sessions/revoke", request{SessionID: id}, &response{})
}
func (c *Client) RevokeGrant(x context.Context, t, id string) error {
	return c.call(x, t, "grants/revoke", request{GrantID: id}, &response{})
}
func (c *Client) CreateWorkspace(x context.Context, t, n string) (comitia.Workspace, error) {
	var o response
	e := c.call(x, t, "workspaces/create", request{Name: n}, &o)
	if o.Workspace == nil {
		return comitia.Workspace{}, missing(e)
	}
	return *o.Workspace, e
}
func (c *Client) CreateContext(x context.Context, t string, i comitia.CreateContextInput) (comitia.WorkContext, error) {
	var o response
	e := c.call(x, t, "contexts/create", request{CreateContext: &i}, &o)
	if o.Context == nil {
		return comitia.WorkContext{}, missing(e)
	}
	return *o.Context, e
}
func (c *Client) GetContext(x context.Context, t, id string) (comitia.WorkContext, error) {
	var o response
	e := c.call(x, t, "contexts/get", request{ContextID: id}, &o)
	if o.Context == nil {
		return comitia.WorkContext{}, missing(e)
	}
	return *o.Context, e
}
func (c *Client) AddReference(x context.Context, t string, i comitia.AddReferenceInput) (comitia.Reference, error) {
	var o response
	e := c.call(x, t, "references/add", request{Reference: &i}, &o)
	if o.Reference == nil {
		return comitia.Reference{}, missing(e)
	}
	return *o.Reference, e
}
func (c *Client) GrantContext(x context.Context, t string, i comitia.GrantInput) (comitia.Grant, error) {
	var o response
	e := c.call(x, t, "grants/create", request{Grant: &i}, &o)
	if o.Grant == nil {
		return comitia.Grant{}, missing(e)
	}
	return *o.Grant, e
}
func (c *Client) AppendMessage(x context.Context, t, id string, i comitia.MessageInput) (comitia.Message, error) {
	var o response
	e := c.call(x, t, "messages/append", request{ContextID: id, Message: &i}, &o)
	if o.Message == nil {
		return comitia.Message{}, missing(e)
	}
	return *o.Message, e
}
func (c *Client) ReplayMessages(x context.Context, t, id string, a int64, l int) ([]comitia.Message, error) {
	var o response
	e := c.call(x, t, "messages/replay", request{ContextID: id, After: a, Limit: l}, &o)
	return o.Messages, e
}
func (c *Client) AckMessages(x context.Context, t, id string, s int64) error {
	return c.call(x, t, "messages/ack", request{ContextID: id, After: s}, &response{})
}
func (c *Client) MessageCursor(x context.Context, t, id string) (int64, error) {
	var o response
	e := c.call(x, t, "messages/cursor", request{ContextID: id}, &o)
	if o.Cursor == nil {
		return 0, missing(e)
	}
	return *o.Cursor, e
}
func (c *Client) CreateTask(x context.Context, t, id string, i comitia.TaskInput) (comitia.Task, error) {
	var o response
	e := c.call(x, t, "tasks/create", request{ContextID: id, Task: &i}, &o)
	if o.Task == nil {
		return comitia.Task{}, missing(e)
	}
	return *o.Task, e
}
func (c *Client) GetTask(x context.Context, t, id string) (comitia.Task, error) {
	var o response
	e := c.call(x, t, "tasks/get", request{TaskID: id}, &o)
	if o.Task == nil {
		return comitia.Task{}, missing(e)
	}
	return *o.Task, e
}
func (c *Client) ListTasks(x context.Context, t, id string, status comitia.TaskStatus, limit int) ([]comitia.Task, error) {
	var o response
	e := c.call(x, t, "tasks/list", request{ContextID: id, Status: status, Limit: limit}, &o)
	return o.Tasks, e
}

func (c *Client) ListParticipants(ctx context.Context, token, cid string, limit int) ([]comitia.Participant, error) {
	var out response
	err := c.call(ctx, token, "participants/list", request{ContextID: cid, Limit: limit}, &out)
	return out.Participants, err
}
func (c *Client) ClaimTask(x context.Context, t, id string, d time.Duration) (comitia.TaskClaim, error) {
	var o response
	e := c.call(x, t, "tasks/claim", request{TaskID: id, TTL: d}, &o)
	if o.Claim == nil {
		return comitia.TaskClaim{}, missing(e)
	}
	return *o.Claim, e
}
func (c *Client) RenewClaim(x context.Context, t, id, k string, v int64, d time.Duration) (comitia.TaskClaim, error) {
	var o response
	e := c.call(x, t, "tasks/renew", request{TaskID: id, ClaimToken: k, LeaseVersion: v, TTL: d}, &o)
	if o.Claim == nil {
		return comitia.TaskClaim{}, missing(e)
	}
	return *o.Claim, e
}
func (c *Client) CompleteTask(x context.Context, t, id, k string, v int64) (comitia.Task, error) {
	var o response
	e := c.call(x, t, "tasks/complete", request{TaskID: id, ClaimToken: k, LeaseVersion: v}, &o)
	if o.Task == nil {
		return comitia.Task{}, missing(e)
	}
	return *o.Task, e
}

var _ comitia.API = (*Client)(nil)

func (c *Client) ListContexts(ctx context.Context, token, afterID string, limit int) ([]comitia.WorkContext, error) {
	var out response
	err := c.call(ctx, token, "contexts/list", request{AfterID: afterID, Limit: limit}, &out)
	return out.Contexts, err
}
