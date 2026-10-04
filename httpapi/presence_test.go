package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

func TestHTTPInstancePresence(t *testing.T) {
	st, err := comitia.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "local", Clock: func() time.Time { return now }})
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
	server := httptest.NewServer(NewHandler(svc))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := client.ListInstances(ctx, owner.Token, c.ID)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty: %v %v", rows, err)
	}
	resume := json.RawMessage(`{"harness":"claude","session_id":"harness","pid":123,"large":9007199254740993}`)
	if err := client.SetInstanceResume(ctx, owner.Token, c.ID, &comitia.AgentInstance{ID: "worker", Label: "HTTP worker"}, resume); err != nil {
		t.Fatal(err)
	}
	worker := "worker"
	for _, call := range []func() error{
		func() error {
			_, err := client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "x", Body: "x", AgentInstance: &comitia.AgentInstance{ID: "worker", Label: "HTTP worker"}})
			return err
		},
		func() error {
			_, err := client.ReplayMessages(ctx, owner.Token, c.ID, 0, 10, comitia.MessageFilters{AgentInstance: &worker})
			return err
		},
		func() error { _, err := client.MessageCursor(ctx, owner.Token, c.ID, "worker"); return err },
		func() error { return client.AckMessages(ctx, owner.Token, c.ID, 0, "worker") },
	} {
		now = now.Add(time.Second)
		if err := call(); err != nil {
			t.Fatal(err)
		}
		rows, err := client.ListInstances(ctx, owner.Token, c.ID)
		if err != nil || len(rows) != 1 || rows[0].Label != "HTTP worker" || string(rows[0].Resume) != string(resume) || !rows[0].LastSeen.Equal(now) {
			t.Fatalf("roundtrip: %v %v", rows, err)
		}
	}
	if err := client.SetInstanceResume(ctx, owner.Token, c.ID, &comitia.AgentInstance{ID: "worker"}, json.RawMessage(`[]`)); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}
	if _, err := client.ListInstances(ctx, "invalid", c.ID); !errors.Is(err, comitia.ErrUnauthenticated) {
		t.Fatalf("auth: %v", err)
	}
	if _, err := client.ListInstances(ctx, owner.Token, "unreadable"); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("scope: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/instances/resume", strings.NewReader(`{"context_id":"`+c.ID+`","instance":{"id":"worker","unknown":true},"resume":{}}`))
	req.Header.Set("Authorization", "Bearer "+owner.Token)
	req.Header.Set("Content-Type", "application/json")
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", response.StatusCode)
	}
}
