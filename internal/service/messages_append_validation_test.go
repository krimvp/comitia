package service

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

	"github.com/krimvp/comitia/internal/evaluation"

	"github.com/krimvp/comitia/internal/schema"

	"github.com/krimvp/comitia/internal/model"
)

func TestAppendRejectsDuplicateNestedAlias(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "evaluation"})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"evaluation_id":"eval","outcome":"accepted","grader":{"ID":"grader"},"grader":{"kind":"human"},"criteria":[{"name":"test","outcome":"passed","evidence_refs":["log"]}]}`)
	m, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "duplicate", Envelope: &model.MessageEnvelope{Schema: model.EvaluationResultSchema, TaskID: task.ID, Payload: raw}})
	if !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("hidden noncanonical nested field accepted: err=%v persisted=%+v", err, m.Envelope)
	}
}

func TestHistoricalEvaluationParsingAndReport(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "evaluation"})
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(`{"Evaluation_id":"eval","outcome":"accepted","grader":{"kind":"human","id":"grader"},"criteria":[{"name":"test","outcome":"passed","evidence_refs":["log"]}]}`)
	m := model.Message{ID: model.NewID(), ContextID: f.context.ID, SessionID: f.admin.Session.ID, DedupeKey: "historical-eval", CreatedAt: f.now.UTC(), Envelope: &model.MessageEnvelope{Schema: model.EvaluationResultSchema, TaskID: task.ID, Payload: raw}}
	if err := f.store.Update(ctx, func(w model.WriteStore) error { _, err := w.PutMessage(m); return err }); err != nil {
		t.Fatal(err)
	}
	replay, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100, model.MessageFilters{Schema: model.EvaluationResultSchema})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(replay[0].Envelope.Payload, raw) {
		t.Fatal("historical bytes changed")
	}
	if result, err := evaluation.ParseEvaluationResult(replay[0]); err != nil || result.EvaluationID != "eval" {
		t.Errorf("historical evaluation parser rejects previously valid record: %v", err)
	}
	if report, err := evaluation.BuildEvaluationReport(replay); err != nil || report.TotalResults != 1 || report.Outcomes.Accepted != 1 {
		t.Errorf("historical evaluation report fails: %v", err)
	}
	if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "new-alias", Envelope: m.Envelope}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("new alias accepted: %v", err)
	}
	legacyDuplicate := m
	legacyDuplicate.Envelope = &model.MessageEnvelope{Schema: model.EvaluationResultSchema, TaskID: task.ID, Payload: json.RawMessage(`{"Evaluation_id":"eval","outcome":"accepted","grader":{"ID":"grader"},"grader":{"kind":"human"},"criteria":[{"name":"test","outcome":"passed","evidence_refs":["log"]}]}`)}
	if _, err := evaluation.ParseEvaluationResult(legacyDuplicate); err != nil {
		t.Fatalf("historical duplicate merging lost: %v", err)
	}
	if _, err := evaluation.BuildEvaluationReport([]model.Message{legacyDuplicate}); err != nil {
		t.Fatalf("historical duplicate report: %v", err)
	}
	invalid := m
	invalid.Envelope = &model.MessageEnvelope{Schema: model.EvaluationResultSchema, TaskID: task.ID, Payload: json.RawMessage(strings.Replace(string(raw), `"outcome":"passed"`, `"outcome":"failed"`, 1))}
	if _, err := evaluation.ParseEvaluationResult(invalid); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("historical semantics not checked: %v", err)
	}
	if _, err := evaluation.BuildEvaluationReport([]model.Message{invalid}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("report semantics not checked: %v", err)
	}

}

func TestBuiltinAppendRejectsDuplicateKeys(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "duplicates"})
	if err != nil {
		t.Fatal(err)
	}
	for schema, descriptor := range f.svc.messageSchemaDescriptors {
		if descriptor.ServerAuthored {
			// Appends are rejected before payload validation; the conformance corpus covers its validator.
			continue
		}
		t.Run(schema, func(t *testing.T) {
			for i, example := range descriptor.Examples {
				var object map[string]json.RawMessage
				if err := json.Unmarshal(example.Envelope.Payload, &object); err != nil {
					t.Fatal(err)
				}
				keys := make([]string, 0, len(object))
				for key := range object {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				for _, key := range keys {
					encoded, _ := json.Marshal(key)
					raw := json.RawMessage(fmt.Sprintf("{%s:%s,%s", encoded, object[key], example.Envelope.Payload[1:]))
					env := example.Envelope
					env.TaskID = task.ID
					env.Payload = raw
					if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: fmt.Sprintf("%s-%d-%s", schema, i, key), Body: example.Body, Envelope: &env}); !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "duplicate key") {
						t.Fatalf("duplicate %s accepted: %v", raw, err)
					}
				}
			}
		})
	}
	eval := `{"evaluation_id":"eval","outcome":"accepted","grader":{"kind":"human","id":"grader"},"criteria":[{"name":"test","outcome":"passed","evidence_refs":["log"]}]}`
	samples := []struct{ name, schema, raw string }{
		{"grader", model.EvaluationResultSchema, strings.Replace(eval, `"id":"grader"`, `"id":"grader","id":"grader"`, 1)},
		{"escaped-key", model.EvaluationResultSchema, strings.Replace(eval, `"id":"grader"`, `"id":"grader","\u0069d":"grader"`, 1)},
		{"criterion", model.EvaluationResultSchema, strings.Replace(eval, `"name":"test"`, `"name":"test","name":"test"`, 1)},
		{"cost-pointer", model.EvaluationResultSchema, eval[:len(eval)-1] + `,"cost":{"amount_micros":1,"amount_micros":2,"currency":"USD"}}`},
		{"dictionary", model.ExecutionEventSchema, `{"run_id":"run","event_sequence":1,"kind":"run_started","dimensions":{"MixedCase":"a","MixedCase":"b"}}`},
	}
	for _, sample := range samples {
		t.Run(sample.name, func(t *testing.T) {
			if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: sample.name, Envelope: &model.MessageEnvelope{Schema: sample.schema, TaskID: task.ID, Payload: json.RawMessage(sample.raw)}}); !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "duplicate key") {
				t.Fatalf("duplicate append: %v", err)
			}
		})
	}
	// Only the task-created event precedes the rejected appends.
	replay, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100)
	if err != nil || len(replay) != 1 || replay[0].Envelope.Schema != model.TaskEventSchema {
		t.Fatalf("rejected payload persisted: %+v %v", replay, err)
	}
}

func TestAppendPayloadScansDictionaryValues(t *testing.T) {
	type item struct {
		Name string `json:"name"`
	}
	type payload struct {
		Values map[string]*item `json:"values"`
	}
	for _, raw := range []string{
		`{"values":{"MixedCase":{"name":"a","name":"b"}}}`,
		`{"values":{"MixedCase":{"Name":"a"}}}`,
		`{"values":{"MixedCase":{"name":"a"},"MixedCase":{"name":"b"}}}`,
	} {
		if err := schema.StrictAppendPayload(json.RawMessage(raw), &payload{}); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("invalid dictionary accepted: %s %v", raw, err)
		}
	}
	if err := schema.StrictAppendPayload(json.RawMessage(`{"values":{"MixedCase":{"name":"a"}}}`), &payload{}); err != nil {
		t.Fatal(err)
	}
	// Scan even object values that will subsequently fail typed decoding.
	if err := schema.ExactPayloadFields(json.RawMessage(`{"values":{"key":[{"id":1,"id":2}]}}`), reflect.TypeOf(&payload{})); !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("untyped nested duplicate: %v", err)
	}
}
