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

func TestMessageSchemaDiscoveryInBothRepresentations(t *testing.T) {
	svc, _, owner, _, _ := administrationServer(t)
	h := NewHandler(svc)
	post := func(path, body, format string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/v1/"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+owner.Token)
		if format != "" {
			r.Header.Set(wireHeader, format)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, format := range []string{"", canonicalWire} {
		w := post("messages/schemas/list", `{}`, format)
		var list struct {
			MessageSchemas []map[string]json.RawMessage `json:"message_schemas"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || w.Code != 200 || len(list.MessageSchemas) == 0 {
			t.Fatalf("format=%q list: %d %s", format, w.Code, w.Body.String())
		}
		for _, d := range list.MessageSchemas {
			for _, key := range []string{"id", "payload_schema", "body_required", "requirements", "examples"} {
				if d[key] == nil || string(d[key]) == "null" {
					t.Fatalf("format=%q descriptor lacks %s: %s", format, key, w.Body.String())
				}
			}
		}
		w = post("messages/schemas/get", `{"schema":"comitia.discussion.v1"}`, format)
		var get struct {
			MessageSchema map[string]json.RawMessage `json:"message_schema"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &get); err != nil || w.Code != 200 || string(get.MessageSchema["id"]) != `"comitia.discussion.v1"` || string(get.MessageSchema["body_required"]) != "true" {
			t.Fatalf("format=%q get: %d %s", format, w.Code, w.Body.String())
		}
		if w := post("messages/schemas/get", `{"schema":"unknown.v1"}`, format); w.Code != 404 {
			t.Fatalf("format=%q unknown schema: %d %s", format, w.Code, w.Body.String())
		}
	}
	// Endpoint DTOs keep the new members on their own routes.
	for _, tc := range []struct{ path, body string }{
		{"messages/schemas/list", `{"schema":"comitia.discussion.v1"}`},
		{"messages/schemas/get", `{"schema":"comitia.discussion.v1","context_id":"c"}`},
		{"participants/list", `{"context_id":"c","strict_recipients":true}`},
		{"messages/cursor", `{"context_id":"c","strict_recipients":true}`},
	} {
		if w := post(tc.path, tc.body, canonicalWire); w.Code != 400 {
			t.Fatalf("%s accepted misplaced field: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
}

// strictFixture returns a context with one strict match and one message that
// matches the instance but also names another session.
func strictFixture(t *testing.T, client comitia.API, svc *comitia.Service, owner comitia.Credential, c comitia.WorkContext, now time.Time) comitia.Message {
	t.Helper()
	ctx := context.Background()
	want, err := client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "strict-match", Body: "for reviewer", Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{"reviewer"}, Payload: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	_, other, _, err := svc.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "other", TTL: 45 * time.Minute, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionRead, ExpiresAt: now.Add(30 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "strict-other", Body: "other session", Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{"reviewer"}, RecipientSessionIDs: []string{other.Session.ID}, Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	return want
}

// preSchemaHandler reproduces the endpoint-DTO Handler before schema discovery
// and strict recipients: no schema routes, and the same strict decoder rejects
// the new read member.
func preSchemaHandler(next http.Handler) http.Handler {
	type read struct {
		ContextID      string   `json:"context_id"`
		After          int64    `json:"after,omitempty"`
		MaxBytes       int      `json:"max_bytes,omitempty"`
		Page           bool     `json:"page,omitempty"`
		AgentInstance  string   `json:"agent_instance,omitempty"`
		ForInstance    string   `json:"for_instance,omitempty"`
		FromInstance   string   `json:"from_instance,omitempty"`
		TaskID         string   `json:"task_id,omitempty"`
		Schema         string   `json:"schema,omitempty"`
		Limit          *int     `json:"limit,omitempty"`
		TimeoutSeconds *float64 `json:"timeout_seconds,omitempty"`
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/v1/messages/schemas/") {
			writeError(w, comitia.ErrNotFound, http.StatusNotFound)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err == nil && (r.URL.Path == "/v1/messages/replay" || r.URL.Path == "/v1/messages/wait") {
			err = decodeRequest(bytes.NewReader(body), new(read))
		}
		if err != nil {
			writeError(w, err, 0)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r)
	})
}

func TestStrictRecipientsAndSchemasAcrossUpgradeOrders(t *testing.T) {
	ctx := context.Background()
	for _, preSchema := range []bool{false, true} {
		t.Run(map[bool]string{false: "current-handler", true: "pre-schema-handler"}[preSchema], func(t *testing.T) {
			svc, admin, owner, c, now := administrationServer(t)
			want := strictFixture(t, admin, svc, owner, c, now)
			var h http.Handler = NewHandler(svc)
			if preSchema {
				h = preSchemaHandler(h)
			}
			server := httptest.NewServer(h)
			defer server.Close()
			client, err := NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
			if err != nil {
				t.Fatal(err)
			}
			// Ordinary filtered reads keep their original request shape.
			if loose, err := client.ReplayMessages(ctx, owner.Token, c.ID, 0, 100, comitia.MessageFilters{ForInstance: "reviewer"}); err != nil || len(loose) != 2 {
				t.Fatalf("loose replay: %v %v", loose, err)
			}
			strict := comitia.MessageFilters{ForInstance: "reviewer", StrictRecipients: true}
			reads := map[string]func() ([]comitia.Message, error){
				"replay": func() ([]comitia.Message, error) {
					return client.ReplayMessages(ctx, owner.Token, c.ID, 0, 100, strict)
				},
				"wait": func() ([]comitia.Message, error) { return client.WaitMessages(ctx, owner.Token, c.ID, 0, 0, strict) },
				"replay-page": func() ([]comitia.Message, error) {
					p, err := client.ReplayMessagesPage(ctx, owner.Token, c.ID, 0, 100, strict)
					return p.Messages, err
				},
				"wait-page": func() ([]comitia.Message, error) {
					p, err := client.WaitMessagesPage(ctx, owner.Token, c.ID, 0, 0, strict)
					return p.Messages, err
				},
			}
			for name, read := range reads {
				messages, err := read()
				if preSchema {
					// Never silently widened to the unfiltered recipient match.
					if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "strict_recipients unsupported by this server") || messages != nil {
						t.Fatalf("%s against old server: %v %v", name, messages, err)
					}
					continue
				}
				if err != nil || len(messages) != 1 || messages[0].ID != want.ID {
					t.Fatalf("%s strict: %v %v", name, messages, err)
				}
			}
			schemas, err := client.ListMessageSchemas(ctx, owner.Token)
			descriptor, getErr := client.GetMessageSchema(ctx, owner.Token, "comitia.discussion.v1")
			_, unknownErr := client.GetMessageSchema(ctx, owner.Token, "unknown.v1")
			if preSchema {
				for _, err := range []error{err, getErr, unknownErr} {
					if !errors.Is(err, comitia.ErrInvalid) || errors.Is(err, comitia.ErrNotFound) || !strings.Contains(err.Error(), "message schema discovery unsupported by this server") {
						t.Fatalf("schema discovery against old server: %v", err)
					}
				}
				return
			}
			if err != nil || len(schemas) == 0 || getErr != nil || descriptor.ID != "comitia.discussion.v1" || !descriptor.BodyRequired || descriptor.Requirements == nil || descriptor.Examples == nil {
				t.Fatalf("schemas: %d %v %+v %v", len(schemas), err, descriptor, getErr)
			}
			if !errors.Is(unknownErr, comitia.ErrNotFound) {
				t.Fatalf("unknown schema: %v", unknownErr)
			}
		})
	}
}

func TestStrictRecipientsRequestShape(t *testing.T) {
	// The member is omitted unless requested, so older servers accept ordinary reads.
	for _, strict := range []bool{false, true} {
		var seen []byte
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			seen, _ = io.ReadAll(r.Body)
			fmt.Fprint(w, `{"messages":[]}`)
		}))
		client, err := NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.ReplayMessages(context.Background(), "caller", "c", 0, 1, comitia.MessageFilters{ForInstance: "reviewer", StrictRecipients: strict}); err != nil {
			t.Fatal(err)
		}
		server.Close()
		if bytes.Contains(seen, []byte("strict_recipients")) != strict {
			t.Fatalf("strict=%v request=%s", strict, seen)
		}
	}
}
