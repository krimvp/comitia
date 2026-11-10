package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHeaderParsingAcceptsJSONParametersAndSchemeCase(t *testing.T) {
	h := NewHandler(fakeAPI{})
	for _, tc := range []struct {
		auth, contentType string
		status            int
	}{
		{"Bearer admin", "application/json; charset=utf-8", http.StatusOK},
		{"Bearer admin", "Application/JSON", http.StatusOK},
		{"Bearer admin", "application/json;charset=UTF-8", http.StatusOK},
		{"bearer admin", "application/json", http.StatusOK},
		{"BEARER admin", "application/json", http.StatusOK},
		{"Bearer admin", "application/json; charset=latin1", http.StatusBadRequest},
		{"Bearer admin", "text/json", http.StatusBadRequest},
		{"Bearer admin", "application/json; charset", http.StatusBadRequest},
		{"Basic admin", "application/json", http.StatusUnauthorized},
		{"bearer  admin", "application/json", http.StatusUnauthorized},
	} {
		r := httptest.NewRequest(http.MethodPost, "/v1/workspaces/create", strings.NewReader(`{"name":"w"}`))
		r.Header.Set("Authorization", tc.auth)
		r.Header.Set("Content-Type", tc.contentType)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%q %q: status %d body %s", tc.auth, tc.contentType, w.Code, w.Body.String())
		}
	}
}

// Like MCP ack_messages, negotiated REST ack names the sequence instead of
// acking 0; unnegotiated legacy clients, which omit an ack of 0, still work.
func TestRESTAckRequiresAfter(t *testing.T) {
	svc, client, owner, c, _ := administrationServer(t)
	server := httptest.NewServer(NewHandler(svc))
	t.Cleanup(server.Close)
	canonical := true
	post := func(body string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/messages/ack", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+owner.Token)
		req.Header.Set("Content-Type", "application/json")
		if canonical {
			req.Header.Set(wireHeader, canonicalWire)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(raw)
	}
	if status, body := post(`{"context_id":"` + c.ID + `"}`); status != http.StatusBadRequest || !strings.Contains(body, "after required") {
		t.Fatalf("missing after: %d %s", status, body)
	}
	if status, body := post(`{"context_id":"` + c.ID + `","after":0}`); status != http.StatusOK {
		t.Fatalf("explicit zero: %d %s", status, body)
	}
	if err := client.AckMessages(context.Background(), owner.Token, c.ID, 0); err != nil {
		t.Fatalf("client ack zero: %v", err)
	}
	canonical = false
	if status, body := post(`{"context_id":"` + c.ID + `"}`); status != http.StatusOK {
		t.Fatalf("legacy omitted after: %d %s", status, body)
	}
}
