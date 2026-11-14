package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestMessageSchemaPayloadConformance(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	descriptors, err := f.svc.ListMessageSchemas(context.Background(), f.admin.Token)
	if err != nil {
		t.Fatal(err)
	}
	for _, descriptor := range descriptors {
		t.Run(descriptor.ID, func(t *testing.T) {
			schema, err := jsonschema.UnmarshalJSON(bytes.NewReader(descriptor.PayloadSchema))
			if err != nil {
				t.Fatal(err)
			}
			compiler := jsonschema.NewCompiler()
			compiler.UseLoader(jsonschema.SchemeURLLoader{})
			url := "urn:comitia:message-schema:" + descriptor.ID
			if err := compiler.AddResource(url, schema); err != nil {
				t.Fatal(err)
			}
			compiled, err := compiler.Compile(url)
			if err != nil {
				t.Fatal(err)
			}
			validator := f.svc.messageSchemas[descriptor.ID]
			check := func(t *testing.T, raw []byte, semanticGap ...string) {
				t.Helper()
				payload, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
				schemaErr := compiled.Validate(payload)
				validatorErr := validator(raw)
				if validatorErr == nil && schemaErr != nil {
					t.Fatalf("server-accepted payload rejected by schema %s: %v", raw, schemaErr)
				}
				if len(semanticGap) > 0 {
					if !strings.Contains(strings.ToLower(strings.Join(descriptor.Requirements, " ")), strings.ToLower(semanticGap[0])) {
						t.Fatalf("undocumented semantic gap %q", semanticGap[0])
					}
				} else if schemaErr == nil && validatorErr != nil {
					t.Fatalf("untagged schema looseness for payload %s: server validator=%v", raw, validatorErr)
				}
			}
			// Every published example contributes the same corpus to both implementations.
			// Mutate every top-level declared property, including optional absent fields.
			properties := schema.(map[string]any)["properties"].(map[string]any)
			for i, example := range descriptor.Examples {
				var base map[string]json.RawMessage
				if err := json.Unmarshal(example.Envelope.Payload, &base); err != nil {
					t.Fatal(err)
				}
				t.Run(fmt.Sprintf("example-%d", i), func(t *testing.T) { check(t, example.Envelope.Payload) })
				for _, sample := range payloadCaseVariants(example.Envelope.Payload) {
					t.Run(fmt.Sprintf("example-%d/mis-cased/%s", i, sample.path), func(t *testing.T) { check(t, sample.raw) })
				}
				for field := range properties {
					// Optional aliases still must be rejected when their value is null.
					t.Run(fmt.Sprintf("example-%d/mis-cased-optional/%s", i, field), func(t *testing.T) {
						candidate := make(map[string]json.RawMessage, len(base)+1)
						for key, raw := range base {
							candidate[key] = raw
						}
						candidate[strings.ToUpper(field)] = json.RawMessage("null")
						raw, _ := json.Marshal(candidate)
						check(t, raw)
					})
					for _, value := range []string{"absent", "null", `""`, `" \t\n"`, `"\u00a0"`, `"\u2003"`, `"value"`, `[]`, `[null]`, `[""]`, `["value"]`, `{}`, `true`, `-1`, `0`, `1`, `1.5`} {
						t.Run(fmt.Sprintf("example-%d/%s/%s", i, field, value), func(t *testing.T) {
							candidate := make(map[string]json.RawMessage, len(base))
							for key, raw := range base {
								candidate[key] = raw
							}
							if value == "absent" {
								delete(candidate, field)
							} else {
								candidate[field] = json.RawMessage(value)
							}
							raw, err := json.Marshal(candidate)
							if err != nil {
								t.Fatal(err)
							}
							if (descriptor.ID == model.ExecutionEventSchema && field == "occurred_at" || descriptor.ID == model.RequestSchema && field == "deadline" || descriptor.ID == model.MergeCertificateSchema && (field == "issued_at" || field == "expires_at")) && (value == `" \t\n"` || value == `"\u00a0"` || value == `"\u2003"` || value == `"value"`) {
								check(t, raw, "RFC3339")
							} else {
								check(t, raw)
							}
						})
					}
				}
				t.Run(fmt.Sprintf("example-%d/unknown-field", i), func(t *testing.T) {
					base["unknown"] = json.RawMessage(`true`)
					raw, _ := json.Marshal(base)
					check(t, raw)
				})
			}
			for _, sample := range messagePayloadSemanticCorpus()[descriptor.ID] {
				t.Run(sample.name, func(t *testing.T) {
					if sample.gap == "" {
						check(t, []byte(sample.payload))
					} else {
						check(t, []byte(sample.payload), sample.gap)
					}
				})
			}
			t.Run("empty-object", func(t *testing.T) { check(t, []byte(`{}`)) })
		})
	}
}

func TestVerificationNullableArtifactsWithTask(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "verification"})
	if err != nil {
		t.Fatal(err)
	}
	for i, artifacts := range []string{"absent", "null", "[]"} {
		payload := map[string]any{"status": "passed", "checks": []string{"go test"}, "evidence_refs": []string{"artifact:test-log"}}
		if artifacts == "null" {
			payload["artifact_refs"] = nil
		}
		if artifacts == "[]" {
			payload["artifact_refs"] = []string{}
		}
		raw, _ := json.Marshal(payload)
		envelope := &model.MessageEnvelope{Schema: "comitia.verification-result.v1", TaskID: task.ID, Payload: raw}
		_, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: fmt.Sprint(i), Envelope: envelope})
		if (err == nil) != (artifacts != "[]") {
			t.Fatalf("task-backed %s: %v", artifacts, err)
		}
		envelope.TaskID = ""
		if _, err := f.svc.normalizeEnvelope(envelope, ""); err == nil {
			t.Fatalf("taskless %s accepted", artifacts)
		}
	}
}

type messagePayloadSample struct{ name, payload, gap string }

func messagePayloadSemanticCorpus() map[string][]messagePayloadSample {
	return map[string][]messagePayloadSample{
		model.ApprovalSchema: {
			{"changes-requested", `{"artifact_refs":["artifact:patch"],"verdict":"changes-requested","checks":["test"],"blocking_count":1}`, ""},
			{"changes-with-zero", `{"artifact_refs":["artifact:patch"],"verdict":"changes-requested","checks":["test"],"blocking_count":0}`, ""},
			{"short-git-ref", `{"artifact_refs":["git:abc"],"verdict":"approved","checks":["test"],"blocking_count":0}`, ""},
			{"integral-decimal-lexeme", `{"artifact_refs":["artifact:patch"],"verdict":"approved","checks":["test"],"blocking_count":0.0}`, "Integer JSON lexemes"},
		},
		model.EvaluationResultSchema: {
			{"null-cost-amount", `{"evaluation_id":"eval","outcome":"accepted","grader":{"kind":"human","id":"grader"},"criteria":[{"name":"test","outcome":"passed","evidence_refs":["log"]}],"cost":{"amount_micros":null,"currency":"USD"}}`, ""},
			{"not-run", `{"evaluation_id":"eval","outcome":"inconclusive","grader":{"kind":"human","id":"grader"},"criteria":[{"name":"test","outcome":"not_run","reason":"offline","evidence_refs":[null]}]}`, ""},
			{"missing-criterion-evidence", `{"evaluation_id":"eval","outcome":"accepted","grader":{"kind":"human","id":"grader"},"criteria":[{"name":"test","outcome":"passed","evidence_refs":null}]}`, "criteria need nonempty evidence_refs"},
			{"duplicate-trimmed-name", `{"evaluation_id":"eval","outcome":"accepted","grader":{"kind":"human","id":"grader"},"criteria":[{"name":"test","outcome":"passed","evidence_refs":["log"]},{"name":" test ","outcome":"passed","evidence_refs":["other"]}]}`, "Trimmed criterion names"},
			{"outcome-mismatch", `{"evaluation_id":"eval","outcome":"accepted","grader":{"kind":"human","id":"grader"},"criteria":[{"name":"test","outcome":"failed","evidence_refs":["log"]}]}`, "accepted requires all criteria passed"},
		},
		model.ExecutionEventSchema: {
			{"cost-with-currency", `{"run_id":"run","event_sequence":1,"kind":"run_started","cost_micros":0,"currency":"USD","cost_source":"configured"}`, ""},
			{"tool-usage", `{"run_id":"run","event_sequence":1,"kind":"tool_usage","tool_name":"go test"}`, ""},
			{"tool-without-name", `{"run_id":"run","event_sequence":1,"kind":"tool_usage","tool_name":null}`, ""},
			{"finished", `{"run_id":"run","event_sequence":1,"kind":"run_finished","status":"succeeded"}`, ""},
			{"finished-without-status", `{"run_id":"run","event_sequence":1,"kind":"run_finished","status":null}`, ""},
			{"integral-decimal-lexeme", `{"run_id":"run","event_sequence":1.0,"kind":"run_started"}`, "Integer JSON lexemes"},
			{"valid-Go-single-digit-hour", `{"run_id":"run","event_sequence":1,"kind":"run_started","occurred_at":"2026-10-04T1:00:00Z"}`, ""},
			{"invalid-calendar", `{"run_id":"run","event_sequence":1,"kind":"run_started","occurred_at":"2026-02-30T12:00:00Z"}`, "RFC3339"},
		},
		model.TaskEventSchema: {
			{"created", `{"kind":"created","status":"pending","lease_version":0}`, ""},
			{"empty-previous", `{"kind":"created","status":"pending","previous_status":"","lease_version":0}`, ""},
			{"unknown-status", `{"kind":"claimed","status":"done","lease_version":1}`, ""},
			{"instance-without-id", `{"kind":"claimed","status":"leased","lease_version":1,"lease_owner_instance":{"label":"worker"}}`, ""},
			{"invalid-instance-id", `{"kind":"claimed","status":"leased","lease_version":1,"lease_owner_instance":{"id":"bad id"}}`, ""},
			{"mis-cased-instance-id", `{"kind":"claimed","status":"leased","lease_version":1,"lease_owner_instance":{"ID":"worker-1"}}`, ""},
			{"integral-decimal-lexeme", `{"kind":"renewed","status":"leased","lease_version":2.0}`, "Integer JSON lexemes"},
		},
		"comitia.finding.v1": {
			{"priority-and-size", `{"severity":"error","summary":"s","priority":"P0","size":"L"}`, ""},
			{"null-priority-and-size", `{"severity":"info","summary":"s","priority":null,"size":null}`, ""},
			{"lowercase-priority", `{"severity":"info","summary":"s","priority":"p1"}`, ""},
			{"unknown-priority", `{"severity":"info","summary":"s","priority":"P4"}`, ""},
			{"empty-size", `{"severity":"info","summary":"s","size":""}`, ""},
			{"unknown-size", `{"severity":"info","summary":"s","size":"XL"}`, ""},
		},
		model.StanceSchema: {
			{"disagree-with-note", `{"stance":"disagree","note":"Breaks old clients"}`, ""},
			{"null-note", `{"stance":"refine","note":null}`, ""},
			{"blank-note", `{"stance":"agree","note":"\u00a0"}`, ""},
			{"unknown-stance", `{"stance":"abstain"}`, ""},
			{"mis-cased-stance", `{"stance":"Agree"}`, ""},
		},
		model.TaskStatusSchema: {
			{"failed", `{"state":"failed","summary":"Tests failed"}`, ""},
			{"cancelled", `{"state":"cancelled","summary":"Superseded"}`, ""},
			{"summary-byte-bound", `{"state":"done","summary":"` + strings.Repeat("é", 2049) + `"}`, "4096 bytes"},
			{"links-byte-bound", `{"state":"done","summary":"done","links":["` + strings.Repeat("é", 513) + `"]}`, "1024 bytes"},
		},
	}
}

func TestServerTaskEventsMatchDescriptor(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	descriptor, err := f.svc.GetMessageSchema(ctx, f.admin.Token, model.TaskEventSchema)
	if err != nil || !descriptor.ServerAuthored {
		t.Fatalf("descriptor: %+v %v", descriptor, err)
	}
	schema, err := jsonschema.UnmarshalJSON(bytes.NewReader(descriptor.PayloadSchema))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	url := "urn:comitia:message-schema:" + model.TaskEventSchema
	if err := compiler.AddResource(url, schema); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(url)
	if err != nil {
		t.Fatal(err)
	}
	released, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "released"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.svc.ClaimTask(ctx, f.admin.Token, released.ID, time.Hour, model.AgentInstance{ID: "worker-1", Label: "Worker"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ReleaseTask(ctx, f.admin.Token, released.ID, claim.Token, claim.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	failed, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "failed"})
	if err != nil {
		t.Fatal(err)
	}
	if claim, err = f.svc.ClaimTask(ctx, f.admin.Token, failed.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.FailTask(ctx, f.admin.Token, failed.ID, model.TaskOutcomeInput{ClaimToken: claim.Token, LeaseVersion: claim.Task.LeaseVersion, Reason: "tests failed"}); err != nil {
		t.Fatal(err)
	}
	completed, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "completed"})
	if err != nil {
		t.Fatal(err)
	}
	if claim, err = f.svc.ClaimTask(ctx, f.admin.Token, completed.ID, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CompleteTask(ctx, f.admin.Token, completed.ID, claim.Token, claim.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	cancelled, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "cancelled"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CancelTask(ctx, f.admin.Token, cancelled.ID, model.TaskOutcomeInput{Reason: "unneeded"}); err != nil {
		t.Fatal(err)
	}
	events, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100, model.MessageFilters{Schema: model.TaskEventSchema})
	if err != nil || len(events) != 11 {
		t.Fatalf("events: %d %v", len(events), err)
	}
	for _, event := range events {
		payload, err := jsonschema.UnmarshalJSON(bytes.NewReader(event.Envelope.Payload))
		if err != nil {
			t.Fatal(err)
		}
		if err := compiled.Validate(payload); err != nil {
			t.Fatalf("schema rejects server event %s: %v", event.Envelope.Payload, err)
		}
		if err := f.svc.messageSchemas[model.TaskEventSchema](event.Envelope.Payload); err != nil {
			t.Fatalf("validator rejects server event %s: %v", event.Envelope.Payload, err)
		}
	}
}
