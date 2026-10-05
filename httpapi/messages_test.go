package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
)

func TestTypedHTTPMessagesAndParticipantDiscovery(t *testing.T) {
	st, err := sqlite.OpenSQLite(t.TempDir() + "/http.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "local"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "local", AdminName: "owner", SessionTTL: time.Hour})
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
	ts := httptest.NewServer(NewHandler(svc))
	defer ts.Close()
	client, err := NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	descriptors, err := client.ListMessageSchemas(ctx, owner.Token)
	if err != nil || len(descriptors) != 17 {
		t.Fatalf("HTTP schemas: %v %v", descriptors, err)
	}
	if event, err := client.GetMessageSchema(ctx, owner.Token, comitia.TaskEventSchema); err != nil || !event.ServerAuthored {
		t.Fatalf("HTTP task-event schema: %+v %v", event, err)
	}
	descriptor, err := client.GetMessageSchema(ctx, owner.Token, "comitia.discussion.v1")
	if err != nil || !descriptor.BodyRequired || len(descriptor.Examples) != 1 {
		t.Fatalf("HTTP descriptor: %+v %v", descriptor, err)
	}
	if _, err := client.GetMessageSchema(ctx, owner.Token, "unknown.v1"); !errors.Is(err, comitia.ErrNotFound) {
		t.Fatalf("unknown schema: %v", err)
	}
	if _, err := client.ListMessageSchemas(ctx, "invalid"); !errors.Is(err, comitia.ErrUnauthenticated) {
		t.Fatalf("discovery auth: %v", err)
	}
	ps, err := client.ListParticipants(ctx, owner.Token, c.ID, 100)
	if err != nil || len(ps) != 1 || ps[0].SessionID != owner.Session.ID {
		t.Fatalf("participants: %v %v", ps, err)
	}
	m, err := client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "typed", Body: "hello", AgentInstance: &comitia.AgentInstance{ID: "worker", Label: "HTTP worker"}, Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{"reviewer"}, RecipientSessionIDs: []string{ps[0].SessionID}, Payload: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := client.ReplayMessages(ctx, owner.Token, c.ID, 0, 100)
	if err != nil || len(msgs) != 1 || msgs[0].Envelope == nil || msgs[0].ID != m.ID {
		t.Fatalf("replay: %v %v", msgs, err)
	}
	if msgs[0].AgentInstance == nil || msgs[0].AgentInstance.ID != "worker" || msgs[0].AgentInstance.Label != "HTTP worker" || !msgs[0].AgentInstanceSelfReported || len(msgs[0].Envelope.RecipientInstances) != 1 {
		t.Fatalf("HTTP lost claim: %v", msgs[0])
	}
	if err = client.AckMessages(ctx, owner.Token, c.ID, m.Sequence, "worker"); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		instance string
		want     int64
	}{{"worker", m.Sequence}, {"reviewer", 0}, {"", 0}} {
		got, err := client.MessageCursor(ctx, owner.Token, c.ID, check.instance)
		if err != nil || got != check.want {
			t.Fatalf("HTTP cursor %q: %d %v", check.instance, got, err)
		}
	}
	if _, err = client.ListParticipants(ctx, "invalid", c.ID, 100); !errors.Is(err, comitia.ErrUnauthenticated) {
		t.Fatalf("unauthenticated discovery: %v", err)
	}
	_, other, _, err := svc.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "other", TTL: 45 * time.Minute, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(30 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "compound-other", Body: "other session", Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{"reviewer"}, RecipientSessionIDs: []string{other.Session.ID}, Payload: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	filter := comitia.MessageFilters{ForInstance: "reviewer", StrictRecipients: true}
	for _, wait := range []bool{false, true} {
		var messages []comitia.Message
		var page comitia.MessagePage
		if wait {
			messages, err = client.WaitMessages(ctx, owner.Token, c.ID, 0, 0, filter)
			if err == nil {
				page, err = client.WaitMessagesPage(ctx, owner.Token, c.ID, 0, 0, filter)
			}
		} else {
			messages, err = client.ReplayMessages(ctx, owner.Token, c.ID, 0, 100, filter)
			if err == nil {
				page, err = client.ReplayMessagesPage(ctx, owner.Token, c.ID, 0, 1, filter)
			}
		}
		if err != nil || len(messages) != 1 || messages[0].ID != m.ID || len(page.Messages) != 1 || page.HasMore {
			t.Fatalf("HTTP strict wait=%t: %v %+v %v", wait, messages, page, err)
		}
	}
	legacy, err := client.ReplayMessages(ctx, owner.Token, c.ID, 0, 100, comitia.MessageFilters{ForInstance: "reviewer"})
	if err != nil || len(legacy) != 2 {
		t.Fatalf("HTTP legacy: %v %v", legacy, err)
	}
	if _, err := client.ReplayMessages(ctx, owner.Token, c.ID, 0, 100, comitia.MessageFilters{StrictRecipients: true}); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("HTTP missing selector: %v", err)
	}
	// Caller-supplied sender metadata is not part of the envelope contract.
	body := `{"context_id":"` + c.ID + `","message":{"DedupeKey":"spoof","Body":"x","envelope":{"schema":"comitia.discussion.v1","from":"owner","payload":{}}}}`
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/messages/append", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+owner.Token)
	req.Header.Set("Content-Type", "application/json")
	res, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("sender spoof field accepted: %d", res.StatusCode)
	}
}

type legacyMessageDiscoveryAPI struct{ comitia.API }

var _ comitia.API = legacyMessageDiscoveryAPI{}

func TestHTTPMessageSchemaDiscoveryLegacyAPI(t *testing.T) {
	ts := httptest.NewServer(NewHandler(legacyMessageDiscoveryAPI{}))
	defer ts.Close()
	client, err := NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ListMessageSchemas(context.Background(), "fixed"); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("unsupported list: %v", err)
	}
	if _, err := client.GetMessageSchema(context.Background(), "fixed", "comitia.discussion.v1"); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("unsupported get: %v", err)
	}
}

// An old server (merge base 172a7aa) rejects the review-panel additions with
// these exact validator errors; everything else reaches a real handler.
func TestReviewPanelRequestsReportOldServerUnsupported(t *testing.T) {
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "local"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "local", AdminName: "owner", SessionTTL: time.Hour})
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
	root, err := svc.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "root", Body: "proposal"})
	if err != nil {
		t.Fatal(err)
	}
	real := NewHandler(svc)
	old := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var req struct {
			Message struct {
				Envelope *struct {
					Schema  string                     `json:"schema"`
					Payload map[string]json.RawMessage `json:"payload"`
				} `json:"envelope"`
			} `json:"message"`
		}
		_ = json.Unmarshal(raw, &req)
		if env := req.Message.Envelope; strings.HasSuffix(r.URL.Path, "/messages/append") && env != nil {
			if env.Schema == comitia.StanceSchema {
				writeError(rw, comitia.Invalidf("schema must name a registered message schema"), http.StatusBadRequest)
				return
			}
			for _, field := range []string{"priority", "size"} {
				if _, ok := env.Payload[field]; ok && env.Schema == "comitia.finding.v1" {
					writeError(rw, comitia.Invalidf("payload: unknown field %q; keys are case-sensitive", field), http.StatusBadRequest)
					return
				}
			}
		}
		real.ServeHTTP(rw, r)
	})
	ts := httptest.NewServer(old)
	defer ts.Close()
	client, err := NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ name, schema, reply, payload, want string }{
		{"legacy-finding", "comitia.finding.v1", "", `{"severity":"info","summary":"s"}`, ""},
		{"priority", "comitia.finding.v1", "", `{"severity":"info","summary":"s","priority":"P1"}`, "comitia.finding.v1 priority unsupported by this server"},
		{"size", "comitia.finding.v1", "", `{"severity":"info","summary":"s","size":"S"}`, "comitia.finding.v1 size unsupported by this server"},
		{"stance", comitia.StanceSchema, root.ID, `{"stance":"agree"}`, comitia.StanceSchema + " unsupported by this server"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: tc.name, Envelope: &comitia.MessageEnvelope{Schema: tc.schema, ReplyTo: tc.reply, Payload: json.RawMessage(tc.payload)}})
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v want %q", err, tc.want)
			}
		})
	}
	// Ordinary unknown-field errors on findings stay invalid input, not unsupported.
	_, err = client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "typo", Envelope: &comitia.MessageEnvelope{Schema: "comitia.finding.v1", Payload: json.RawMessage(`{"severity":"info","summary":"s","prority":"P1"}`)}})
	if !errors.Is(err, comitia.ErrInvalid) || strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("typo: %v", err)
	}
}
