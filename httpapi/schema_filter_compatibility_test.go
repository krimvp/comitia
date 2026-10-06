package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	c "github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/sqlite"
)

func TestLegacySchemaConsumerUpgradeOrder(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	reader, err := c.New(c.Config{Store: st, BootstrapCredential: "temporary-bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	_, owner, err := reader.Bootstrap(ctx, c.BootstrapInput{Credential: "temporary-bootstrap", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := reader.CreateWorkspace(ctx, owner.Token, "review")
	if err != nil {
		t.Fatal(err)
	}
	room, err := reader.CreateContext(ctx, owner.Token, c.CreateContextInput{WorkspaceID: ws.ID, Name: "review"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.NewHandler(reader))
	defer server.Close()
	client, err := httpapi.NewClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	filters := c.MessageFilters{Schema: "example.future.v1"}
	t.Run("consumer-before-registry", func(t *testing.T) {
		for _, wait := range []bool{false, true} {
			var messages []c.Message
			var err error
			if wait {
				messages, err = client.WaitMessages(ctx, owner.Token, room.ID, 0, 0, filters)
			} else {
				messages, err = client.ReplayMessages(ctx, owner.Token, room.ID, 0, 100, filters)
			}
			if err != nil || len(messages) != 0 {
				t.Errorf("wait=%v: legacy pre-registration filter should return an empty page; messages=%d err=%v", wait, len(messages), err)
			}
		}
	})
	writer, err := c.New(c.Config{Store: st, MessageSchemas: map[string]c.PayloadValidator{"example.future.v1": func(json.RawMessage) error { return nil }}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = writer.AppendMessage(ctx, owner.Token, room.ID, c.MessageInput{DedupeKey: "future", Envelope: &c.MessageEnvelope{Schema: "example.future.v1", Payload: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Run("historical-extension-without-validator", func(t *testing.T) {
		for _, wait := range []bool{false, true} {
			var messages []c.Message
			var err error
			if wait {
				messages, err = client.WaitMessages(ctx, owner.Token, room.ID, 0, 0, filters)
			} else {
				messages, err = client.ReplayMessages(ctx, owner.Token, room.ID, 0, 100, filters)
			}
			if err != nil || len(messages) != 1 {
				t.Fatalf("wait=%v: historical extension read should not need an append validator; messages=%d err=%v", wait, len(messages), err)
			}
		}
	})
}

func TestRecipientFieldIndexUsesRequestOrder(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := c.New(c.Config{Store: st, BootstrapCredential: "temporary-bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	_, owner, err := svc.Bootstrap(ctx, c.BootstrapInput{Credential: "temporary-bootstrap", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, owner.Token, "review")
	if err != nil {
		t.Fatal(err)
	}
	room, err := svc.CreateContext(ctx, owner.Token, c.CreateContextInput{WorkspaceID: ws.ID, Name: "review"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.NewHandler(svc))
	defer server.Close()
	client, err := httpapi.NewClient(server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.AppendMessage(ctx, owner.Token, room.ID, c.MessageInput{DedupeKey: "index", Body: "note", Envelope: &c.MessageEnvelope{Schema: "comitia.discussion.v1", Payload: json.RawMessage(`{}`), RecipientSessionIDs: []string{owner.Session.ID, "00000000000000000000000000000000"}}})
	if err == nil || !strings.Contains(err.Error(), "recipient_session_ids[1]") {
		t.Fatalf("missing recipient is request index 1; got %v", err)
	}
}
