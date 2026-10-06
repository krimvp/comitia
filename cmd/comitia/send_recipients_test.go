package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
)

func TestSendRecipientsCLI(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "remote"}[remote], func(t *testing.T) {
			api, token, cid, _ := watchFixture(t)
			ctx := context.Background()
			participants, err := api.ListParticipants(ctx, token, cid, 100)
			if err != nil {
				t.Fatal(err)
			}
			if remote {
				server := httptest.NewServer(httpapi.NewHandler(api))
				defer server.Close()
				api, err = httpapi.NewClient(server.URL, server.Client())
				if err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"--context-id", cid, "--dedupe-key", "routed", "--body", "note", "--recipient-session-id", participants[0].SessionID, "--recipient-instance", "z", "--recipient-instance", "a"}
			var out bytes.Buffer
			if err = runMessages(ctx, config{credential: token}, api, json.NewEncoder(&out), "send", args); err != nil {
				t.Fatal(err)
			}
			var m comitia.Message
			if err = json.Unmarshal(out.Bytes(), &m); err != nil {
				t.Fatal(err)
			}
			if m.Envelope == nil || !reflect.DeepEqual(m.Envelope.RecipientInstances, []string{"a", "z"}) || !reflect.DeepEqual(m.Envelope.RecipientSessionIDs, []string{participants[0].SessionID}) {
				t.Fatalf("CLI recipients: %+v", m)
			}
			path := filepath.Join(t.TempDir(), "envelope.json")
			if err = os.WriteFile(path, []byte(`{"schema":"comitia.discussion.v1","recipient_instances":["a"],"payload":{}}`), 0600); err != nil {
				t.Fatal(err)
			}
			if err = runMessages(ctx, config{credential: token}, api, json.NewEncoder(&out), "send", append(args, "--envelope-file", path)); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("ambiguous recipients: %v", err)
			}
		})
	}
	// Parsing help must not read the envelope file or call the API.
	if err := runMessages(context.Background(), config{parseOnly: true}, nil, nil, "send", []string{"--recipient-session-id", "s", "--recipient-instance", "i", "--envelope-file", "missing.json"}); err != nil {
		t.Fatalf("parseOnly: %v", err)
	}
}
