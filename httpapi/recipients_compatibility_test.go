package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/httpapi/internal/legacy"
	"github.com/krimvp/comitia/sqlite"
)

func TestTopLevelRecipientsHTTPUpgradeOrders(t *testing.T) {
	for _, newServer := range []bool{false, true} {
		for _, newClient := range []bool{false, true} {
			t.Run(fmt.Sprintf("new-server=%v/new-client=%v", newServer, newClient), func(t *testing.T) {
				st, err := sqlite.OpenSQLite(":memory:")
				if err != nil {
					t.Fatal(err)
				}
				defer st.Close()
				svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "boot"})
				if err != nil {
					t.Fatal(err)
				}
				ctx := context.Background()
				_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "boot", AdminName: "owner", SessionTTL: time.Hour})
				if err != nil {
					t.Fatal(err)
				}
				w, err := svc.CreateWorkspace(ctx, owner.Token, "w")
				if err != nil {
					t.Fatal(err)
				}
				c, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: w.ID, Name: "c"})
				if err != nil {
					t.Fatal(err)
				}
				handler := legacy.NewHandler(svc)
				if newServer {
					handler = httpapi.NewHandler(svc)
				}
				requests := 0
				var seen map[string]json.RawMessage
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v1/messages/append" {
						requests++
						raw, _ := io.ReadAll(r.Body)
						r.Body = io.NopCloser(strings.NewReader(string(raw)))
						var q struct {
							Message map[string]json.RawMessage `json:"message"`
						}
						if err := json.Unmarshal(raw, &q); err != nil {
							t.Error(err)
						}
						seen = q.Message
					}
					handler.ServeHTTP(w, r)
				}))
				defer server.Close()
				var client comitia.API
				if newClient {
					client, err = httpapi.NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
				} else {
					client, err = legacy.NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
				}
				if err != nil {
					t.Fatal(err)
				}
				for _, field := range []string{"recipient_session_ids", "recipient_instances"} {
					in := comitia.MessageInput{DedupeKey: field, Body: "note"}
					if newClient {
						if field == "recipient_session_ids" {
							in.RecipientSessionIDs = []string{owner.Session.ID}
						} else {
							in.RecipientInstances = []string{"reviewer"}
						}
					} else {
						in.Envelope = &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", Payload: json.RawMessage(`{}`), RecipientSessionIDs: []string{owner.Session.ID}, RecipientInstances: []string{"reviewer"}}
					}
					requests = 0
					m, err := client.AppendMessage(ctx, owner.Token, c.ID, in)
					if requests != 1 {
						t.Fatalf("append retried: %d", requests)
					}
					if newClient && !newServer {
						if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "top-level "+field+" unsupported by this server") || m.ID != "" {
							t.Fatalf("old server: %+v %v", m, err)
						}
						messages, err := svc.ReplayMessages(ctx, owner.Token, c.ID, 0, 100)
						if err != nil || len(messages) != 0 {
							t.Fatalf("unsupported append persisted: %v %v", messages, err)
						}
					} else {
						if err != nil || m.Envelope == nil {
							t.Fatalf("append: %+v %v", m, err)
						}
						filters := comitia.MessageFilters{ForInstance: "reviewer", StrictRecipients: true}
						messages, err := svc.WaitMessages(ctx, owner.Token, c.ID, m.Sequence-1, 0, filters)
						if err != nil || len(messages) != 1 || messages[0].ID != m.ID {
							t.Fatalf("routed read: %v %v", messages, err)
						}
						if newClient && field == "recipient_session_ids" && !reflect.DeepEqual(m.Envelope.RecipientSessionIDs, in.RecipientSessionIDs) {
							t.Fatalf("lost recipients: %+v", m)
						}
						if newClient && field == "recipient_instances" && !reflect.DeepEqual(m.Envelope.RecipientInstances, in.RecipientInstances) {
							t.Fatalf("lost recipients: %+v", m)
						}
					}
				}
				if newClient && newServer {
					// A payload field with the same name is a schema error, not a
					// rejection of the new request field by an old server.
					_, err := client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "bad-payload", Body: "note", RecipientInstances: []string{"reviewer"}, Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", Payload: json.RawMessage(`{"recipient_instances":[]}`)}})
					if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "payload:") || strings.Contains(err.Error(), "unsupported") {
						t.Fatalf("payload error misreported as unsupported: %v", err)
					}
				}
				// Null/empty recipients must be omitted so ordinary sends reach old servers.
				for i, in := range []comitia.MessageInput{{DedupeKey: "nil", Body: "plain"}, {DedupeKey: "empty", Body: "plain", RecipientSessionIDs: []string{}, RecipientInstances: []string{}}} {
					m, err := client.AppendMessage(ctx, owner.Token, c.ID, in)
					if err != nil || m.Envelope != nil {
						t.Fatalf("legacy plain %d: %+v %v", i, m, err)
					}
					for _, field := range []string{"recipient_session_ids", "recipient_instances"} {
						if _, exists := seen[field]; exists {
							t.Fatalf("empty %s sent: %s", field, seen[field])
						}
					}
				}
			})
		}
	}
}
