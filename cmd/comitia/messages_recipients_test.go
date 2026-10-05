package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
)

func TestRemoteCLIStrictRecipients(t *testing.T) {
	ctx := context.Background()
	st, err := comitia.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "boot"})
	if err != nil {
		t.Fatal(err)
	}
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "boot", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, owner.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	wc, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	_, other, _, err := svc.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "other", TTL: 45 * time.Minute, Grants: []comitia.DelegatedGrant{{ContextID: wc.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(30 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, sid := range []string{other.Session.ID, owner.Session.ID} {
		_, err := svc.AppendMessage(ctx, owner.Token, wc.ID, comitia.MessageInput{DedupeKey: sid, Body: "hello", Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientInstances: []string{"shared"}, RecipientSessionIDs: []string{sid}, Payload: json.RawMessage(`{}`)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(httpapi.NewHandler(svc))
	defer ts.Close()
	client, err := httpapi.NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"replay", "wait"} {
		var out bytes.Buffer
		args := []string{"--context-id", wc.ID, "--for-instance", "shared", "--strict-recipients", "--timeout-seconds", "0"}
		if err := runMessages(ctx, config{credential: owner.Token}, client, json.NewEncoder(&out), cmd, args); err != nil {
			t.Fatal(err)
		}
		var messages []comitia.Message
		if err := json.Unmarshal(out.Bytes(), &messages); err != nil || len(messages) != 1 || messages[0].Sequence != 2 {
			t.Fatalf("strict CLI %s: %v %v", cmd, messages, err)
		}
	}
}
