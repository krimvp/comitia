package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/krimvp/comitia"
)

const maxRequestBody = 1 << 20

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
	if !jsonMediaType(r.Header.Get("Content-Type")) {
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
	canonical := r.Header.Get(wireHeader) == canonicalWire
	ctx := r.Context()
	if canonical {
		ctx = context.WithValue(ctx, canonicalKey{}, true)
	}
	out, err := call(ctx, token, r.Body)
	if err != nil {
		operation := comitia.SafeOperation(route)
		info := comitia.SafeError(operation, err, requestID)
		w.Header().Set("X-Request-ID", info.RequestID)
		comitia.ReportFailure(h.diagnostic, operation, err, info.RequestID, time.Since(started))
		writeOperationError(w, err, 0, operation, requestID)
		return
	}
	if canonical {
		w.Header().Set(wireHeader, canonicalWire)
	} else {
		out = legacyValue(reflect.ValueOf(out))
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}

// canonicalKey marks a request that negotiated the canonical wire, whose
// stricter request rules legacy clients predate.
type canonicalKey struct{}

func canonicalRequest(ctx context.Context) bool {
	v, _ := ctx.Value(canonicalKey{}).(bool)
	return v
}

// jsonMediaType accepts application/json in any case, with parameters, as
// long as any charset is UTF-8.
func jsonMediaType(v string) bool {
	t, params, err := mime.ParseMediaType(v)
	charset, has := params["charset"]
	return err == nil && t == "application/json" && (!has || strings.EqualFold(charset, "utf-8"))
}

// bearer reads an Authorization header; the scheme is case-insensitive.
func bearer(v string) (string, bool) {
	p := strings.Split(v, " ")
	returnValue := ""
	if len(p) == 2 && strings.EqualFold(p[0], "Bearer") && p[1] != "" {
		returnValue = p[1]
	}
	return returnValue, returnValue != ""
}
