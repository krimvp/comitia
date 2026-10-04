package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestV1ResponseRepresentationRequiresOptIn(t *testing.T) {
	h := NewHandler(new(wireProbe))
	for _, format := range []string{"", "unknown", canonicalWire} {
		r := httptest.NewRequest("POST", "/v1/tasks/claim", strings.NewReader(`{"task_id":"t","lease_seconds":60}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer caller")
		if format != "" {
			r.Header.Set(wireHeader, format)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var result map[string]map[string]json.RawMessage
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		wantTask, wantToken := "Task", "Token"
		if format == canonicalWire {
			wantTask, wantToken = "task", "token"
		}
		if w.Code != 200 || result["claim"][wantTask] == nil || result["claim"][wantToken] == nil {
			t.Fatalf("format=%q status=%d response=%s", format, w.Code, w.Body.String())
		}
		if format == canonicalWire && w.Header().Get(wireHeader) != canonicalWire {
			t.Fatal("canonical response did not confirm negotiation")
		}
		if format != canonicalWire && w.Header().Get(wireHeader) == canonicalWire {
			t.Fatal("legacy response claimed canonical format")
		}
		// The default DTO keeps the old tagged instance fields, but never publishes
		// a task's private lease token hash in either representation.
		if strings.Contains(w.Body.String(), "LeaseTokenHash") || strings.Contains(w.Body.String(), "lease_token_hash") {
			t.Fatalf("private hash exposed: %s", w.Body.String())
		}
	}
}
