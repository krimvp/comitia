package httpapi

import (
	"context"
	"github.com/krimvp/comitia"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientReadsLegacyResponseNames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/contexts/list":
			w.Write([]byte(`{"contexts":[{"ID":"c","WorkspaceID":"w","Name":"context","ParentReadable":true,"future_metadata":{}}]}`))
		case "/v1/sessions/mint":
			w.Write([]byte(`{"credential":{"Session":{"ID":"s","PrincipalID":"p","ExpiresAt":"2026-10-04T13:00:00Z"},"Token":"credential"}}`))
		case "/v1/tasks/claim":
			w.Write([]byte(`{"claim":{"Task":{"ID":"t","LeaseOwnerSessionID":"s","LeaseVersion":1},"Token":"claim"}}`))
		case "/v1/messages/replay":
			w.Write([]byte(`{"page":{"messages":[{"ID":"m","ContextID":"c","Sequence":1,"Body":"hello","envelope":{"schema":"comitia.discussion.v1","payload":{"ID":"opaque"}}}],"next_after":1,"has_more":false,"scanned_through":4},"future_metadata":{}}`))
		case "/v1/messages/wait":
			// Servers before filtered scan progress omit scanned_through.
			w.Write([]byte(`{"page":{"messages":[],"next_after":3,"has_more":false}}`))
		}
	}))
	defer server.Close()
	client, err := NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	contexts, err := client.ListContexts(ctx, "caller", "", 100)
	if err != nil || len(contexts) != 1 || contexts[0].ID != "c" || contexts[0].WorkspaceID != "w" || !contexts[0].ParentReadable {
		t.Fatalf("context: %+v %v", contexts, err)
	}
	credential, err := client.MintSession(ctx, "caller", "p", time.Hour)
	if err != nil || credential.Token != "credential" || credential.Session.ID != "s" || credential.Session.PrincipalID != "p" {
		t.Fatalf("credential: %+v %v", credential.Session, err)
	}
	claim, err := client.ClaimTask(ctx, "caller", "t", time.Minute)
	if err != nil || claim.Token != "claim" || claim.Task.ID != "t" || claim.Task.LeaseVersion != 1 {
		t.Fatalf("claim: %+v %v", claim.Task, err)
	}
	page, err := client.ReplayMessagesPage(ctx, "caller", "c", 0, 100)
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != "m" || page.Messages[0].Body != "hello" || string(page.Messages[0].Envelope.Payload) != `{"ID":"opaque"}` || page.NextAfter != 1 || page.ScannedThrough != 4 {
		t.Fatalf("page: %+v %v", page, err)
	}
	page, err = client.WaitMessagesPage(ctx, "caller", "c", 3, 0)
	if err != nil || page.NextAfter != 3 || page.ScannedThrough != 3 {
		t.Fatalf("page without scan progress: %+v %v", page, err)
	}
}
