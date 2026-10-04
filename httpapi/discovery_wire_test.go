package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

// discoveryFixture adds three tasks and two granted sessions besides the owner.
func discoveryFixture(t *testing.T) (*comitia.Service, comitia.Credential, comitia.WorkContext) {
	t.Helper()
	svc, client, owner, c, now := administrationServer(t)
	ctx := context.Background()
	for _, title := range []string{"one", "two", "three"} {
		if _, err := client.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: title}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"first", "second"} {
		p, err := client.CreatePrincipal(ctx, owner.Token, name)
		if err != nil {
			t.Fatal(err)
		}
		credential, err := client.MintSession(ctx, owner.Token, p.ID, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.GrantContext(ctx, owner.Token, comitia.GrantInput{SessionID: credential.Session.ID, ContextID: c.ID, Permissions: comitia.PermissionRead, ExpiresAt: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	return svc, owner, c
}

func TestDiscoveryPagesInBothRepresentations(t *testing.T) {
	svc, owner, c := discoveryFixture(t)
	h := NewHandler(svc)
	for _, format := range []string{"", canonicalWire} {
		for _, kind := range []string{"tasks", "participants"} {
			r := httptest.NewRequest("POST", "/v1/"+kind+"/list", strings.NewReader(fmt.Sprintf(`{"context_id":%q,"limit":2,"page":true}`, c.ID)))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("Authorization", "Bearer "+owner.Token)
			if format != "" {
				r.Header.Set(wireHeader, format)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			key, id := "task_page", "ID"
			if kind == "participants" {
				key, id = "participant_page", "SessionID"
			}
			if format == canonicalWire {
				id = map[string]string{"tasks": "id", "participants": "session_id"}[kind]
			}
			var result map[string]struct {
				NextAfter string `json:"next_after"`
				HasMore   bool   `json:"has_more"`
			}
			var raw map[string]map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || json.Unmarshal(w.Body.Bytes(), &raw) != nil {
				t.Fatalf("format=%q %s: %s", format, kind, w.Body.String())
			}
			var records []map[string]json.RawMessage
			if err := json.Unmarshal(raw[key][kind], &records); err != nil {
				t.Fatalf("format=%q %s records: %s", format, kind, w.Body.String())
			}
			page := result[key]
			if w.Code != 200 || len(records) != 2 || records[0][id] == nil || page.NextAfter == "" || !page.HasMore || raw[kind] != nil {
				t.Fatalf("format=%q status=%d response=%s", format, w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "LeaseTokenHash") || strings.Contains(w.Body.String(), "lease_token_hash") {
				t.Fatalf("private hash exposed: %s", w.Body.String())
			}
		}
	}
	// Paging members belong only to the discovery list endpoints.
	for _, path := range []string{"contexts/children", "instances/list"} {
		r := httptest.NewRequest("POST", "/v1/"+path, strings.NewReader(fmt.Sprintf(`{"context_id":%q,"page":true}`, c.ID)))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+owner.Token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("%s accepted page: %d %s", path, w.Code, w.Body.String())
		}
	}
}

// prePagingHandler reproduces the endpoint-DTO Handler before discovery paging:
// the same strict decoder rejects the paging request members.
func prePagingHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v1/tasks/list":
			err = decodeRequest(bytes.NewReader(body), new(struct {
				ContextID string             `json:"context_id"`
				Status    comitia.TaskStatus `json:"status,omitempty"`
				Limit     *int               `json:"limit,omitempty"`
			}))
		case "/v1/participants/list":
			err = decodeRequest(bytes.NewReader(body), new(struct {
				ContextID string `json:"context_id"`
				Limit     *int   `json:"limit,omitempty"`
			}))
		}
		if err != nil {
			writeError(w, err, 0)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

func TestDiscoveryClientAcrossUpgradeOrders(t *testing.T) {
	svc, owner, c := discoveryFixture(t)
	ctx := context.Background()
	for _, prePaging := range []bool{false, true} {
		t.Run(map[bool]string{false: "paging-handler", true: "pre-paging-handler"}[prePaging], func(t *testing.T) {
			var h http.Handler = NewHandler(svc)
			if prePaging {
				h = prePagingHandler(h)
			}
			server := httptest.NewServer(h)
			defer server.Close()
			client, err := NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
			if err != nil {
				t.Fatal(err)
			}
			// Plain lists keep their original request shape in both orders.
			if tasks, err := client.ListTasks(ctx, owner.Token, c.ID, "", 100); err != nil || len(tasks) != 3 {
				t.Fatalf("tasks: %d %v", len(tasks), err)
			}
			if participants, err := client.ListParticipants(ctx, owner.Token, c.ID, 100); err != nil || len(participants) != 3 {
				t.Fatalf("participants: %d %v", len(participants), err)
			}
			seen := map[string]bool{}
			for after, pages := "", 0; ; pages++ {
				page, err := comitia.ListTaskPage(ctx, client, owner.Token, c.ID, "", after, 2)
				if err != nil || pages > 2 || page.HasMore && page.NextAfter == after {
					t.Fatalf("task page: %+v %v", page, err)
				}
				for _, task := range page.Tasks {
					if seen[task.ID] || task.LeaseTokenHash != "" {
						t.Fatalf("task repeated or exposed: %+v", task)
					}
					seen[task.ID] = true
				}
				if after = page.NextAfter; !page.HasMore {
					break
				}
			}
			for after, pages := "", 0; ; pages++ {
				page, err := comitia.ListParticipantPage(ctx, client, owner.Token, c.ID, after, 2)
				if err != nil || pages > 2 || page.HasMore && page.NextAfter == after {
					t.Fatalf("participant page: %+v %v", page, err)
				}
				for _, p := range page.Participants {
					if seen[p.SessionID] {
						t.Fatalf("participant repeated: %+v", p)
					}
					seen[p.SessionID] = true
				}
				if after = page.NextAfter; !page.HasMore {
					break
				}
			}
			if len(seen) != 6 {
				t.Fatalf("discovery incomplete: %v", seen)
			}
			// The fallback is only for missing capability, never for invalid input.
			if _, err := client.ListTasksPage(ctx, owner.Token, c.ID, "", "not a cursor", 2); !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "cursor") {
				t.Fatalf("invalid cursor: %v", err)
			}
		})
	}
}
