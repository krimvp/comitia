package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/httpapi/internal/legacy"
)

func TestDedupeConflictDetailsOnCurrentHandler(t *testing.T) {
	for _, newClient := range []bool{false, true} {
		t.Run(map[bool]string{false: "old", true: "new"}[newClient]+"-client", func(t *testing.T) {
			ctx := context.Background()
			store, err := comitia.OpenSQLite(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "boot"})
			if err != nil {
				t.Fatal(err)
			}
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
			_, writer, _, err := svc.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "write-only", TTL: 30 * time.Minute, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionWrite, ExpiresAt: time.Now().Add(30 * time.Minute)}}})
			if err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(httpapi.NewHandler(svc))
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
			original, err := client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "started", Body: "first"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "started", Body: "second"})
			if !errors.Is(err, comitia.ErrConflict) || !strings.Contains(err.Error(), original.ID) {
				t.Fatalf("conflict: %v", err)
			}
			var conflict *comitia.DedupeConflictError
			if newClient && (!errors.As(err, &conflict) || conflict.MessageID != original.ID || conflict.Sequence != original.Sequence || strings.Join(conflict.Differs, ",") != "body") {
				t.Fatalf("typed conflict: %+v %v", conflict, err)
			}
			// A write-only session gets the plain conflict over the wire.
			hidden, err := client.AppendMessage(ctx, writer.Token, c.ID, comitia.MessageInput{DedupeKey: "w", Body: "first"})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.AppendMessage(ctx, writer.Token, c.ID, comitia.MessageInput{DedupeKey: "w", Body: "second"})
			var op *comitia.OperationalError
			if !errors.Is(err, comitia.ErrConflict) || errors.As(err, &conflict) && conflict.MessageID == hidden.ID || !errors.As(err, &op) || op.Info.Details != nil || strings.Contains(err.Error(), hidden.ID) || strings.Contains(err.Error(), "get_message") {
				t.Fatalf("write-only conflict leaked: %v %+v", err, op)
			}
			_, err = client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: strings.Repeat("k", comitia.MaxDedupeKeyBytes+1), Body: "x"})
			if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "at most 256 bytes") {
				t.Fatalf("long key: %v", err)
			}
		})
	}
}

// preDedupeDetailsHandler freezes the pre-#118 /v1 messages/append wire: no key
// cap and a plain conflict without an ID or details.
func preDedupeDetailsHandler() http.Handler {
	bodies := map[string]string{}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q struct {
			Message map[string]any `json:"message"`
		}
		_ = json.NewDecoder(r.Body).Decode(&q)
		field := func(snake, pascal string) string {
			v, ok := q.Message[snake].(string)
			if !ok {
				v, _ = q.Message[pascal].(string)
			}
			return v
		}
		key, body := field("dedupe_key", "DedupeKey"), field("body", "Body")
		w.Header().Set("Content-Type", "application/json")
		if old, ok := bodies[key]; ok && old != body {
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"conflict","message":"conflict","hint":"Reuse the original message with this dedupe key, or use a new key for a different message.","request_id":"00000000000000000000000000000000"}`))
			return
		}
		bodies[key] = body
		k, _ := json.Marshal(key)
		b, _ := json.Marshal(body)
		_, _ = fmt.Fprintf(w, `{"message":{"ID":"c3c40d253c4885d05222fb603272c476","ContextID":"1b70cd6d8e6c3a9761e5acd11b310485","SessionID":"18db5f440ada874e66e48e54f970030c","DedupeKey":%s,"Body":%s,"Sequence":%d,"CreatedAt":"2026-10-05T16:35:56.306228258Z"}}`, k, b, len(bodies))
	})
}

func TestDedupeConflictNewClientOldHandlerFallback(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(preDedupeDetailsHandler())
	defer server.Close()
	client, err := httpapi.NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AppendMessage(ctx, "credential", "context", comitia.MessageInput{DedupeKey: "key", Body: "first"}); err != nil {
		t.Fatal(err)
	}
	_, err = client.AppendMessage(ctx, "credential", "context", comitia.MessageInput{DedupeKey: "key", Body: "second"})
	var typed *comitia.DedupeConflictError
	var op *comitia.OperationalError
	if !errors.Is(err, comitia.ErrConflict) || errors.As(err, &typed) || !errors.As(err, &op) || op.Info.Details != nil || op.Info.Message != "conflict" {
		t.Fatalf("old conflict fallback: %v typed=%+v", err, typed)
	}
	long := strings.Repeat("k", comitia.MaxDedupeKeyBytes+1)
	if m, err := client.AppendMessage(ctx, "credential", "context", comitia.MessageInput{DedupeKey: long, Body: "x"}); err != nil || m.DedupeKey != long {
		t.Fatalf("old server long key: %+v %v", m, err)
	}
}
