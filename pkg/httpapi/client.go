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
	"sync/atomic"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

const maxResponseBody = 16 << 20

// ErrResponseTooLarge is wrapped with ErrInvalid and a hint naming the
// request parameter that shrinks the response.
var ErrResponseTooLarge = errors.New("remote response exceeds 16777216-byte limit")

func responseTooLarge(path string) error {
	hint := "lower limit or request a page"
	if path == "messages/replay" || path == "messages/wait" {
		hint = "retry with a smaller max_bytes or limit"
	}
	return fmt.Errorf("%w: %w; %s", comitia.ErrInvalid, ErrResponseTooLarge, hint)
}

// Client is a remote implementation of comitia.API.
type Client struct {
	base             *url.URL
	http             *http.Client
	InstanceDefaults comitia.InstanceDefaults
	waits            *waitDrain
	querySupported   *atomic.Bool // positive messages/query-support probe
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
	return &Client{base: u, http: hc, InstanceDefaults: defaults, waits: &waitDrain{ended: make(chan struct{})}, querySupported: new(atomic.Bool)}, nil
}
func (c *Client) call(ctx context.Context, token, path string, in, out any) error {
	b, err := json.Marshal(legacyValue(reflect.ValueOf(in)))
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
	req.Header.Set(wireHeader, canonicalWire)
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
		return responseTooLarge(path)
	}
	d := json.NewDecoder(bytes.NewReader(b))
	if resp.StatusCode/100 != 2 {
		var e errorResponse
		if d.Decode(&e) != nil || ensureEOF(d) != nil {
			return fmt.Errorf("http status %d: malformed error response", resp.StatusCode)
		}
		return &comitia.OperationalError{Info: e.ErrorInfo, Cause: remoteCause(e)}
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
	if resp.Header.Get(wireHeader) != canonicalWire {
		legacyCollections(reflect.ValueOf(out).Elem())
	}
	return validateCollections(out)
}

var _ comitia.API = (*Client)(nil)

// Endpoint-DTO servers before discovery paging reject its request members;
// older union servers ignore them and omit the page.
func pagingUnsupported(err error) bool {
	return errors.Is(err, comitia.ErrInvalid) && (strings.Contains(err.Error(), `unknown field "page"`) || strings.Contains(err.Error(), `unknown field "after_id"`))
}

// unknownField reports a strict decoder rejection, from either an endpoint-DTO
// Handler or the older request-union Handler, of a member it predates.
func unknownField(err error, name string) bool {
	return errors.Is(err, comitia.ErrInvalid) && strings.Contains(err.Error(), `unknown field "`+name+`"`)
}
