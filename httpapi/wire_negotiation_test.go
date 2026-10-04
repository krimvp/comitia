package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/krimvp/comitia"
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

func TestFilteredScanProgressInBothRepresentations(t *testing.T) {
	svc, client, owner, c, _ := administrationServer(t)
	m, err := client.AppendMessage(context.Background(), owner.Token, c.ID, comitia.MessageInput{DedupeKey: "skip", Body: "skip"})
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"", canonicalWire} {
		for _, path := range []string{"replay", "wait"} {
			body := fmt.Sprintf(`{"context_id":%q,"page":true,"from_instance":"nobody"}`, c.ID)
			if path == "wait" {
				body = fmt.Sprintf(`{"context_id":%q,"page":true,"from_instance":"nobody","timeout_seconds":0}`, c.ID)
			}
			r := httptest.NewRequest("POST", "/v1/messages/"+path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+owner.Token)
			if format != "" {
				r.Header.Set(wireHeader, format)
			}
			w := httptest.NewRecorder()
			NewHandler(svc).ServeHTTP(w, r)
			var result struct {
				Page map[string]json.RawMessage `json:"page"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || string(result.Page["scanned_through"]) != fmt.Sprint(m.Sequence) || string(result.Page["next_after"]) != "0" {
				t.Fatalf("format=%q path=%s status=%d response=%s", format, path, w.Code, w.Body.String())
			}
		}
	}
}
