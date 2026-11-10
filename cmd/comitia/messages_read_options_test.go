package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

func TestReadCommandCursorSelfAndSchemaList(t *testing.T) {
	api, token, cid, path := watchFixture(t)
	ctx := context.Background()
	task, err := api.CreateTask(ctx, token, cid, comitia.TaskInput{Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	own, err := api.AppendMessage(ctx, token, cid, comitia.MessageInput{DedupeKey: "own", Body: "own", AgentInstance: &comitia.AgentInstance{ID: "reader"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = api.AckMessages(ctx, token, cid, own.Sequence, "reader"); err != nil {
		t.Fatal(err)
	}
	peer, err := api.AppendMessage(ctx, token, cid, comitia.MessageInput{DedupeKey: "peer", Body: "peer", AgentInstance: &comitia.AgentInstance{ID: "peer"}, Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", Payload: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"replay", "wait"} {
		args := []string{"--db", path, "--credential", token, cmd, "--context-id", cid, "--agent-instance", "reader", "--from-cursor", "--exclude-self", "--page", "--timeout-seconds", "0"}
		page := commandAny(t, args...).(map[string]any)
		messages := page["messages"].([]any)
		if len(messages) != 1 || messages[0].(map[string]any)["ID"] != peer.ID || page["next_after"] != float64(peer.Sequence) {
			t.Fatalf("%s cursor self: %v", cmd, page)
		}
		args = []string{"--db", path, "--credential", token, cmd, "--context-id", cid, "--schema", "comitia.discussion.v1", "--schema", comitia.TaskEventSchema, "--task-id", task.ID, "--timeout-seconds", "0"}
		// Task filter keeps the created event; schemas are an OR selector.
		events := commandAny(t, args...).([]any)
		if len(events) != 1 {
			t.Fatalf("%s repeated schema: %v", cmd, events)
		}
		args = []string{"--db", path, "--credential", token, cmd, "--context-id", cid, "--schema", "comitia.discussion.v1", "--schema", comitia.TaskEventSchema, "--timeout-seconds", "0"}
		events = commandAny(t, args...).([]any)
		if len(events) != 2 {
			t.Fatalf("%s schema union: %v", cmd, events)
		}
	}
	cfg := config{credential: token}
	for _, cmd := range []string{"replay", "wait"} {
		if err = runMessages(ctx, cfg, api, json.NewEncoder(io.Discard), cmd, []string{"--context-id", cid, "--from-cursor", "--after", "0"}); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("ambiguous CLI start: %v", err)
		}
	}
	cfg.parseOnly = true
	if err = runMessages(ctx, cfg, nil, nil, "wait", []string{"--context-id", "c", "--from-cursor", "--exclude-self", "--schema", "a", "--schema", "b"}); err != nil {
		t.Fatalf("parseOnly guard: %v", err)
	}
}

type scanWatchAPI struct {
	comitia.MessageAPI
	calls  []int64
	cancel context.CancelFunc
}

func (a *scanWatchAPI) WaitMessagesPage(_ context.Context, _ string, _ string, after int64, _ time.Duration, _ ...comitia.MessageFilters) (comitia.MessagePage, error) {
	a.calls = append(a.calls, after)
	if len(a.calls) == 2 {
		a.cancel()
	}
	return comitia.MessagePage{Messages: []comitia.Message{}, NextAfter: after, ScannedThrough: after + 3}, nil
}
func (a *scanWatchAPI) ReplayMessagesPage(context.Context, string, string, int64, int, ...comitia.MessageFilters) (comitia.MessagePage, error) {
	panic("unused")
}

func TestWatchAdvancesAcrossSkippedEventsWithoutAck(t *testing.T) {
	api, token, cid, _ := watchFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &scanWatchAPI{MessageAPI: api, cancel: cancel}
	var out bytes.Buffer
	if err := watchMessageOutput(ctx, probe, token, cid, "reader", time.Second, comitia.MessageFilters{}, false, false, &out); err != nil {
		t.Fatal(err)
	}
	if len(probe.calls) != 2 || probe.calls[0] != 0 || probe.calls[1] != 3 || out.Len() != 0 {
		t.Fatalf("scan progress: %v output=%q", probe.calls, out.String())
	}
	if cursor, err := api.MessageCursor(context.Background(), token, cid, "reader"); err != nil || cursor != 0 {
		t.Fatalf("manual mode acknowledged: %d %v", cursor, err)
	}
}

func TestWatchAcknowledgesSkippedRenewals(t *testing.T) {
	api, token, cid, _ := watchFixture(t)
	ctx := context.Background()
	task, err := api.CreateTask(ctx, token, cid, comitia.TaskInput{Title: "lease"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := api.ClaimTask(ctx, token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = api.AckMessages(ctx, token, cid, 2, "reader"); err != nil {
		t.Fatal(err)
	}
	if _, err = api.RenewClaim(ctx, token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	probe := &watchAckFailureAPI{MessageAPI: api, onAck: func(sequence int64) {
		if sequence == 3 {
			cancel()
		}
	}}
	// Retain the page capability while intercepting acknowledgements.
	pages := watchPagesWithAck{watchAckFailureAPI: probe, MessagePageAPI: api.(comitia.MessagePageAPI)}
	var output bytes.Buffer
	if err = watchMessages(runCtx, pages, token, cid, "reader", 100*time.Millisecond, comitia.MessageFilters{}, true, &output); err != nil {
		t.Fatal(err)
	}
	if cursor, err := api.MessageCursor(ctx, token, cid, "reader"); err != nil || cursor != 3 || output.Len() != 0 {
		t.Fatalf("renewal-only watch: cursor=%d output=%q %v", cursor, output.String(), err)
	}
}

type watchPagesWithAck struct {
	*watchAckFailureAPI
	comitia.MessagePageAPI
}
