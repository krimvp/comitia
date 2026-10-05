package comitia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

type payloadCaseVariant struct {
	path string
	raw  json.RawMessage
}

// Change one struct-style object key at a time, including nested objects and
// array elements. Dictionary keys are covered separately because they are data.
func payloadCaseVariants(raw json.RawMessage) []payloadCaseVariant {
	var root any
	if json.Unmarshal(raw, &root) != nil {
		panic("invalid test payload")
	}
	var out []payloadCaseVariant
	var visit func(any, string)
	visit = func(value any, path string) {
		switch value := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(value))
			for key := range value {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				entry := value[key]
				for _, alias := range []string{strings.ToUpper(key), strings.ToUpper(key[:1]) + key[1:]} {
					if alias == key {
						continue
					}
					delete(value, key)
					value[alias] = entry
					mutated, _ := json.Marshal(root)
					out = append(out, payloadCaseVariant{strings.TrimPrefix(path+"/"+alias, "/"), mutated})
					delete(value, alias)
					value[key] = entry
				}
				visit(entry, path+"/"+key)
			}
		case []any:
			for i, entry := range value {
				visit(entry, fmt.Sprintf("%s/%d", path, i))
			}
		}
	}
	visit(root, "")
	return out
}

func TestBuiltinPayloadKeysAreCaseSensitive(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "case-sensitive payloads"})
	if err != nil {
		t.Fatal(err)
	}
	descriptors, err := f.svc.ListMessageSchemas(ctx, f.admin.Token)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range descriptors {
		if d.ServerAuthored {
			continue
		}
		t.Run(d.ID, func(t *testing.T) {
			for i, example := range d.Examples {
				env := example.Envelope
				if env.TaskID != "" {
					env.TaskID = task.ID
				}
				canonical, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: fmt.Sprintf("%s-%d", d.ID, i), Body: example.Body, Envelope: &env})
				if err != nil {
					t.Fatalf("canonical append: %v", err)
				}
				var want, got any
				_ = json.Unmarshal(example.Envelope.Payload, &want)
				_ = json.Unmarshal(canonical.Envelope.Payload, &got)
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("stored payload changed: %s", canonical.Envelope.Payload)
				}
				variants := payloadCaseVariants(env.Payload)
				if d.ID == "comitia.discussion.v1" {
					variants = []payloadCaseVariant{{"unexpected-field", json.RawMessage(`{"Unexpected":true}`)}}
				}
				for j, variant := range variants {
					t.Run(fmt.Sprintf("example-%d/%s", i, variant.path), func(t *testing.T) {
						env.Payload = variant.raw
						if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: fmt.Sprintf("%s-%d-%d", d.ID, i, j), Body: example.Body, Envelope: &env}); !errors.Is(err, ErrInvalid) {
							t.Fatalf("mis-cased append %s: %v", variant.raw, err)
						}
					})
				}
			}
		})
	}
}

func TestStrictPayloadExactFields(t *testing.T) {
	type item struct {
		Name string `json:"name"`
	}
	type payload struct {
		Item       *item             `json:"item"`
		Items      []item            `json:"items"`
		Dictionary map[string]string `json:"dictionary"`
		Optional   string            `json:"optional,omitempty"`
	}
	for _, raw := range []string{
		`{"item":{"name":"a"},"items":[{"name":"b"}],"dictionary":{"MixedCase":"value"}}`,
		`{"item":null,"items":null,"dictionary":null,"optional":null}`,
	} {
		if err := strictAppendPayload(json.RawMessage(raw), &payload{}); err != nil {
			t.Fatalf("canonical/null payload: %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"Item":{"name":"a"}}`, `{"item":{"Name":"a"}}`, `{"items":[{"NAME":"b"}]}`,
		`{"OPTIONAL":null}`, `{"optional":"canonical","Optional":"alias"}`,
		`{"item":{"name":"canonical","Name":"alias"}}`,
	} {
		if err := strictAppendPayload(json.RawMessage(raw), &payload{}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("mis-cased payload: %s: %v", raw, err)
		}
	}
}

func TestPayloadCaseRulesPreserveStoredMessages(t *testing.T) {
	path := t.TempDir() + "/messages.db"
	f := setup(t, path)
	defer f.store.Close()
	ctx := context.Background()
	raw := json.RawMessage(`{"Severity":"warning","Summary":"Historical finding"}`)
	historical := Message{ID: id(), ContextID: f.context.ID, SessionID: f.admin.Session.ID, DedupeKey: "historical", CreatedAt: f.now.UTC(), Envelope: &MessageEnvelope{Schema: "comitia.finding.v1", Payload: raw}}
	if err := f.store.Update(ctx, func(w WriteStore) error { _, err := w.PutMessage(historical); return err }); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	svc, err := New(Config{Store: reopened, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100)
	if err != nil || len(replay) != 1 || !bytes.Equal(replay[0].Envelope.Payload, raw) {
		t.Fatalf("historical payload changed: %v %v", replay, err)
	}
	if _, err := svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "new-finding", Envelope: historical.Envelope}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("new mis-cased append: %v", err)
	}
}

func TestExtensionPayloadCaseRules(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	type metric struct {
		Value int `json:"value"`
	}
	svc, err := New(Config{Store: f.store, Clock: func() time.Time { return *f.now }, MessageSchemas: map[string]PayloadValidator{
		"project.custom.v1": func(raw json.RawMessage) error { var p metric; return json.Unmarshal(raw, &p) },
		"project.strict.v1": func(raw json.RawMessage) error { var p metric; return strictPayload(raw, &p) },
	}})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"project.custom.v1", "project.strict.v1"} {
		for _, raw := range []string{`{"value":1}`, `{"Value":1}`} {
			message, err := svc.AppendMessage(context.Background(), f.admin.Token, f.context.ID, MessageInput{DedupeKey: schema + raw, Envelope: &MessageEnvelope{Schema: schema, Payload: json.RawMessage(raw)}})
			if err != nil || !bytes.Equal(message.Envelope.Payload, []byte(raw)) {
				t.Fatalf("extension behavior changed: %+v %v", message, err)
			}
		}
	}
	// Routing dimensions are dictionary data, not struct field names.
	task, err := svc.CreateTask(context.Background(), f.admin.Token, f.context.ID, TaskInput{Title: "run"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AppendMessage(context.Background(), f.admin.Token, f.context.ID, MessageInput{DedupeKey: "dictionary-keys", Envelope: &MessageEnvelope{Schema: ExecutionEventSchema, TaskID: task.ID, Payload: json.RawMessage(`{"run_id":"run","event_sequence":1,"kind":"run_started","dimensions":{"MixedCase":"data","UPPER":"data"}}`)}}); err != nil {
		t.Fatalf("dictionary keys rejected: %v", err)
	}
}
