package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

func TestHTTPFilteredReplayAndWait(t *testing.T) {
	st, err := comitia.OpenSQLite(":memory:")
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
	task, err := svc.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewHandler(svc))
	defer ts.Close()
	client, err := NewClient(ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.http.Timeout <= comitia.MaxMessageWait {
		t.Fatal("default HTTP timeout must allow the longest wait")
	}
	filter := comitia.MessageFilters{ForInstance: "reader", FromInstance: "writer", TaskID: task.ID, Schema: "comitia.discussion.v1"}
	input := comitia.MessageInput{DedupeKey: "1", Body: "ready", AgentInstance: &comitia.AgentInstance{ID: "writer"}, Envelope: &comitia.MessageEnvelope{Schema: filter.Schema, TaskID: task.ID, RecipientInstances: []string{"reader"}, Payload: json.RawMessage(`{}`)}}
	m, err := client.AppendMessage(ctx, owner.Token, c.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		f := filter
		switch i {
		case 1:
			f.ForInstance = "other"
		case 2:
			f.FromInstance = "other"
		case 3:
			f.TaskID = "other"
		case 4:
			f.Schema = "other"
		}
		got, err := client.ReplayMessages(ctx, owner.Token, c.ID, 0, 1, f)
		want := 0
		if i == 0 {
			want = 1
		}
		if err != nil || len(got) != want {
			t.Fatalf("replay filter %+v: %v %v", f, got, err)
		}
		got, err = client.WaitMessages(ctx, owner.Token, c.ID, 0, 0, f)
		if err != nil || len(got) != want {
			t.Fatalf("wait filter %+v: %v %v", f, got, err)
		}
	}
	result := make(chan error, 1)
	go func() {
		got, err := client.WaitMessages(ctx, owner.Token, c.ID, m.Sequence, 2*time.Second, filter)
		if err == nil && (len(got) != 1 || got[0].Sequence != m.Sequence+1) {
			err = errors.New("wrong wait result")
		}
		result <- err
	}()
	input.DedupeKey = "2"
	start := time.Now()
	if _, err := client.AppendMessage(ctx, owner.Token, c.ID, input); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil || time.Since(start) >= time.Second {
			t.Fatalf("append-to-wait: %v (%v)", err, time.Since(start))
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP wait did not return within a second of append")
	}
	got, err := client.WaitMessages(ctx, owner.Token, c.ID, 2, 20*time.Millisecond, filter)
	if err != nil || len(got) != 0 || got == nil {
		t.Fatalf("HTTP timeout: %v %v", got, err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := client.WaitMessages(cancelCtx, owner.Token, c.ID, 2, time.Second); !errors.Is(err, context.Canceled) {
		t.Fatalf("HTTP cancellation: %v", err)
	}
	if _, err := client.WaitMessages(ctx, "invalid", c.ID, 2, time.Second); !errors.Is(err, comitia.ErrUnauthenticated) {
		t.Fatalf("HTTP wait authorization: %v", err)
	}
	if _, err := client.WaitMessages(ctx, owner.Token, c.ID, 2, -time.Second); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("HTTP negative timeout: %v", err)
	}
	if _, err := client.ReplayMessages(ctx, owner.Token, c.ID, 0, 10, filter, filter); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("HTTP filter arity: %v", err)
	}
}
