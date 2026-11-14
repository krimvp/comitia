package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/httpapi"
)

func TestCLIMessageLookupAndScopedQueries(t *testing.T) {
	api, token, cid, _ := watchFixture(t)
	ctx := context.Background()
	root, err := api.AppendMessage(ctx, token, cid, comitia.MessageInput{DedupeKey: "root", Body: "root"})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := api.AppendMessage(ctx, token, cid, comitia.MessageInput{DedupeKey: "reply", Body: "reply", Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", ReplyTo: root.ID, Payload: json.RawMessage(`{}`)}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.NewHandler(api))
	defer server.Close()
	for _, remote := range []bool{false, true} {
		// Use the fixture's local API directly; CLI remote path is exercised fully.
		if !remote {
			var sink bytes.Buffer
			err := runMessages(ctx, config{credential: token}, api, json.NewEncoder(&sink), "message-get", []string{"--context-id", cid, "--message-id", reply.ID})
			if err != nil {
				t.Fatal(err)
			}
			var got comitia.Message
			if err := json.Unmarshal(sink.Bytes(), &got); err != nil || !reflect.DeepEqual(got, reply) {
				t.Fatalf("local lookup: %+v %v", got, err)
			}
			continue
		}
		base := []string{"--server-url", server.URL, "--credential", token}
		got := command(t, append(base, "message-get", "--context-id", cid, "--message-id", reply.ID)...)
		if got["ID"] != reply.ID || got["SessionID"] != reply.SessionID {
			t.Fatalf("remote lookup: %+v", got)
		}
		for _, cmd := range []string{"replay", "wait"} {
			args := append(append([]string{}, base...), cmd, "--context-id", cid, "--reply-to", root.ID, "--thread-id", root.ID, "--from-session", root.SessionID, "--page")
			if cmd == "wait" {
				args = append(args, "--timeout-seconds", "0")
			}
			page := command(t, args...)
			records := page["messages"].([]any)
			if len(records) != 1 || records[0].(map[string]any)["ID"] != reply.ID {
				t.Fatalf("%s filters: %+v", cmd, page)
			}
		}
	}
	if err := runMessages(ctx, config{parseOnly: true}, nil, nil, "message-get", []string{"--context-id", "c", "--message-id", "x"}); err != nil {
		t.Fatalf("parseOnly guard: %v", err)
	}
	if err := runMessages(ctx, config{credential: token}, oldCLIQueryAPI{api}, json.NewEncoder(io.Discard), "replay", []string{"--context-id", cid, "--reply-to", root.ID}); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("legacy API widened: %v", err)
	}
	// A scoped watch still honors its configured business cursor.
	watchCtx, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	if err := watchMessageOutput(watchCtx, api, token, cid, "scoped", time.Millisecond, comitia.MessageFilters{ReplyTo: root.ID}, false, true, io.Discard); err != nil {
		t.Fatal(err)
	}
}

type oldCLIQueryAPI struct{ comitia.API }
