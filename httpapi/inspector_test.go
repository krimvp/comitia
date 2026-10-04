package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/krimvp/comitia"
)

func TestInspectorAssetsAndReadOnlyShell(t *testing.T) {
	h := NewHandler(fakeAPI{})
	for _, path := range []string{"/ui/", "/ui/app.js", "/ui/dom.mjs", "/ui/model.mjs", "/ui/graph.mjs", "/ui/sequence.mjs", "/ui/tree.mjs", "/ui/presence.mjs", "/ui/wire.mjs", "/ui/style.css"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK || w.Body.Len() == 0 {
			t.Fatalf("%s: %d", path, w.Code)
		}
		if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Fatalf("%s missing security headers", path)
		}
	}
	for _, path := range []string{"/ui/credentials.json", "/ui/inspector.go", "/ui/nope"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("unexpected asset %s: %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/ui/", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("shell mutation: %d", w.Code)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodHead, "/ui/", nil))
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatal("HEAD returned a body")
	}
}

func TestInspectorLocalFonts(t *testing.T) {
	h := inspectorHandler()
	for _, name := range []string{"IBMPlexSans-Regular-Latin1.woff2", "IBMPlexSans-SemiBold-Latin1.woff2", "IBMPlexMono-Regular-Latin1.woff2", "IBMPlexMono-SemiBold-Latin1.woff2", "OFL.txt"} {
		path := "/ui/" + name
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			mime := "font/woff2"
			if name == "OFL.txt" {
				mime = "text/plain; charset=utf-8"
			}
			if w.Code != http.StatusOK || w.Header().Get("Content-Type") != mime {
				t.Fatalf("%s %s: status=%d type=%s", method, path, w.Code, w.Header().Get("Content-Type"))
			}
			if !strings.Contains(w.Header().Get("Content-Security-Policy"), "font-src 'self'") {
				t.Fatal("local fonts blocked by CSP")
			}
			if method == http.MethodHead && w.Body.Len() != 0 {
				t.Fatal("font HEAD returned a body")
			}
			if method == http.MethodGet {
				if name == "OFL.txt" && !strings.Contains(w.Body.String(), "SIL OPEN FONT LICENSE") {
					t.Fatal("missing font license")
				}
				if name != "OFL.txt" && !strings.HasPrefix(w.Body.String(), "wOF2") {
					t.Fatal("invalid WOFF2 asset")
				}
			}
		}
	}
	for _, path := range []string{"/ui/nope.woff2", "/ui/README.txt", "/ui/../index.html"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("unexpected font asset %s: %d", path, w.Code)
		}
	}
}

type discoveryAPI struct{ comitia.API }

func (discoveryAPI) ListContexts(_ context.Context, token, after string, limit int) ([]comitia.WorkContext, error) {
	if token != "reader" {
		return nil, comitia.ErrForbidden
	}
	if after != "before" || limit != 3 {
		return nil, comitia.ErrInvalid
	}
	return []comitia.WorkContext{{ID: "visible", Name: "Readable"}}, nil
}

func TestContextDiscoveryHTTPRoundTrip(t *testing.T) {
	ts := httptest.NewServer(NewHandler(discoveryAPI{}))
	defer ts.Close()
	client, err := NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.ListContexts(context.Background(), "reader", "before", 3)
	if err != nil || len(result) != 1 || result[0].ID != "visible" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if _, err := client.ListContexts(context.Background(), "other", "before", 3); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("scope: %v", err)
	}
}
