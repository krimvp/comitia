package comitia

import (
	"encoding/json"
	"strings"
)

func addCoordinationDescriptors(add func(string, PayloadValidator, map[string]any, bool, bool, bool, []string, ...string)) {
	shaField := func() map[string]any {
		return map[string]any{"type": "string", "pattern": "^([0-9a-f]{40}|[0-9a-f]{64})$"}
	}
	text := func() map[string]any { return map[string]any{"type": []string{"string", "null"}} }
	refs := func() map[string]any {
		return map[string]any{"type": []string{"array", "null"}, "items": exactArtifactPayload()}
	}
	ids := func() map[string]any {
		p := payloadStrings()
		p["maxItems"] = 64
		p["uniqueItems"] = true
		return p
	}
	sha := strings.Repeat("a", 40)
	artifact := "pr:owner/repo#1@" + sha
	add(RequestSchema, validRequest, payloadObject(map[string]any{"operation": payloadText(), "instructions": payloadText(), "expected_responders": ids(), "deadline": payloadText(), "artifact_refs": refs()}, "operation", "instructions", "expected_responders", "deadline"), false, false, false,
		[]string{"deadline is an absolute RFC3339 timestamp in the future at first append. Expected responders must be active readable authenticated sessions in this context; IDs are unique, at most 64. Control operations execute externally; instructions must contain no secrets."},
		`{"operation":"control.pause","instructions":"Pause at the next checkpoint","expected_responders":["example-session"],"deadline":"2099-01-01T00:00:00Z"}`)
	receipt := payloadObject(map[string]any{"status": payloadEnum("accepted", "completed", "rejected"), "result_refs": refs(), "reason": text()}, "status")
	receipt["allOf"] = []any{
		payloadCondition("status", map[string]any{"const": "completed"}, payloadFields(map[string]any{"result_refs": map[string]any{"type": "array", "minItems": 1, "items": exactArtifactPayload()}}, "result_refs")),
		payloadCondition("status", map[string]any{"const": "rejected"}, payloadFields(map[string]any{"reason": payloadText()}, "reason")),
	}
	add(ReceiptSchema, validReceipt, receipt, false, false, false,
		[]string{"envelope.reply_to must identify a request in this context; task_id must match. Authenticated sender must be an expected responder. New receipts at or after the deadline are rejected; identical dedupe retries remain recoverable. accepted means accepted for handling, not completed; completed requires durable result_refs."},
		`{"status":"accepted"}`, `{"status":"completed","result_refs":["checkpoint:launch-1/paused"]}`, `{"status":"rejected","reason":"Unsupported control"}`)
	certificate := payloadObject(map[string]any{"policy_id": payloadText(), "artifact_ref": exactArtifactPayload(), "head_sha": shaField(), "approval_ids": ids(), "issued_at": payloadText(), "expires_at": payloadText(), "consumption_key": payloadText()}, "policy_id", "artifact_ref", "head_sha", "approval_ids", "issued_at", "expires_at", "consumption_key")
	add(MergeCertificateSchema, validMergeCertificate, certificate, false, false, false,
		[]string{"artifact_ref is an exact pr reference matching full lowercase head_sha. issued_at and expires_at are RFC3339 and expiry follows issuance. reply_to identifies a request; approval_ids identify approval.v1 messages replying to that request in this context with matching task_id. This schema is shape/correlation only; the external gate must verify authenticated roles, current SHA, freshness and invalidation, reserve consumption once and reconcile GitHub."},
		`{"policy_id":"merge-policy-1","artifact_ref":"`+artifact+`","head_sha":"`+sha+`","approval_ids":["example-approval"],"issued_at":"2026-01-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","consumption_key":"merge-operation-1"}`)
	merge := payloadObject(map[string]any{"operation_id": payloadText(), "status": payloadEnum("merged", "not_merged"), "head_sha": shaField(), "merge_sha": text(), "evidence_refs": map[string]any{"type": "array", "minItems": 1, "items": exactArtifactPayload()}}, "operation_id", "status", "head_sha", "evidence_refs")
	merge["allOf"] = []any{
		payloadCondition("status", map[string]any{"const": "merged"}, payloadFields(map[string]any{"merge_sha": shaField()}, "merge_sha")),
		payloadCondition("status", map[string]any{"const": "not_merged"}, payloadFields(map[string]any{"merge_sha": map[string]any{"enum": []any{"", nil}}})),
	}
	add(MergeReceiptSchema, validMergeReceipt, merge, false, false, false,
		[]string{"reply_to identifies a certificate; task_id, operation_id (certificate consumption_key) and head_sha must match. merged requires a full lowercase merge_sha; not_merged requires empty merge_sha. Evidence identifies external reconciliation; sender must be checked against launcher-owned merger roles, and a receipt is not proof of GitHub state."},
		`{"operation_id":"merge-operation-1","status":"merged","head_sha":"`+sha+`","merge_sha":"`+sha+`","evidence_refs":["github:merge-operation-1"]}`)
}

func cloneMessageSchema(d MessageSchemaDescriptor) MessageSchemaDescriptor {
	raw, _ := json.Marshal(d)
	var out MessageSchemaDescriptor
	_ = json.Unmarshal(raw, &out)
	return out
}

func payloadObject(properties map[string]any, required ...string) map[string]any {
	out := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

// This is unicode.IsSpace, also used by strings.TrimSpace in the validators.
const payloadWhitespace = "\t-\r \u0085\u00a0\u1680\u2000-\u200a\u2028\u2029\u202f\u205f\u3000"

func payloadText() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "pattern": "[^" + payloadWhitespace + "]"}
}

func payloadStrings() map[string]any {
	return map[string]any{"type": "array", "minItems": 1, "items": payloadText()}
}

func payloadEnum(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}

func nullablePayload(schema map[string]any) map[string]any {
	schema["type"] = []string{schema["type"].(string), "null"}
	return schema
}

func payloadCondition(field string, when, then map[string]any) map[string]any {
	return map[string]any{"if": map[string]any{"required": []string{field}, "properties": map[string]any{field: when}}, "then": then}
}

func payloadFields(properties map[string]any, required ...string) map[string]any {
	out := map[string]any{"properties": properties}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func payloadInteger(min int) map[string]any {
	return map[string]any{"type": "integer", "minimum": min, "maximum": int64(9223372036854775807)}
}

// builtinSchema is one entry of the built-in schema registry: the server
// validator and the descriptor published for runtime discovery.
type builtinSchema struct {
	validate   PayloadValidator
	descriptor MessageSchemaDescriptor
}

// builtinMessageSchemaDescriptors returns the descriptor of every built-in schema in the registry.
func builtinMessageSchemaDescriptors() map[string]MessageSchemaDescriptor {
	out := map[string]MessageSchemaDescriptor{}
	for id, schema := range builtinSchemas() {
		out[id] = schema.descriptor
	}
	return out
}

// builtinSchemas is the registry of built-in message schemas, one entry per schema.
func builtinSchemas() map[string]builtinSchema {
	out := map[string]builtinSchema{}
	add := func(id string, validate PayloadValidator, payload map[string]any, body, task, artifacts bool, requirements []string, examples ...string) {
		payload["$schema"] = "https://json-schema.org/draft/2020-12/schema"
		payload["$id"] = "urn:comitia:message-schema:" + id
		raw, _ := json.Marshal(payload)
		d := MessageSchemaDescriptor{ID: id, PayloadSchema: raw, BodyRequired: body, TaskRequired: task, TaskOrArtifactRefs: artifacts, Requirements: requirements, Examples: []MessageSchemaExample{}}
		if d.Requirements == nil {
			d.Requirements = []string{}
		}
		d.Requirements = append(d.Requirements, "The server validator is authoritative; semantic requirements supplement JSON Schema.")
		if id == ApprovalSchema || id == EvaluationResultSchema || id == ExecutionEventSchema || id == TaskEventSchema {
			d.Requirements = append(d.Requirements, "Integer JSON lexemes must be accepted by Go's integer decoder; integral decimal/exponent notation is rejected even though JSON Schema treats it as an integer.")
		}
		for _, example := range examples {
			e := MessageSchemaExample{Envelope: MessageEnvelope{Schema: id, Payload: json.RawMessage(example)}}
			if body {
				e.Body = "Discussion text"
			}
			if task {
				e.Envelope.TaskID = "example-task-id"
			}
			switch id {
			case ReceiptSchema, MergeCertificateSchema:
				e.Envelope.ReplyTo = "example-request-message"
			case StanceSchema:
				e.Envelope.ReplyTo = "example-finding-message"
			case MergeReceiptSchema:
				e.Envelope.ReplyTo = "example-certificate-message"
			}
			d.Examples = append(d.Examples, e)
		}
		d.ServerAuthored = id == TaskEventSchema
		out[id] = builtinSchema{validate: validate, descriptor: d}
	}
	add("comitia.discussion.v1", validDiscussion, payloadObject(map[string]any{}), true, false, false, nil, `{}`)
	add("comitia.assignment.v1", validAssignment, payloadObject(map[string]any{"instructions": payloadText(), "acceptance_criteria": payloadStrings()}, "instructions", "acceptance_criteria"), false, true, false, nil, `{"instructions":"Implement recovery","acceptance_criteria":["Retries are idempotent"]}`)
	add("comitia.review-request.v1", validReviewRequest, payloadObject(map[string]any{"artifact_refs": payloadStrings(), "checks": payloadStrings()}, "artifact_refs", "checks"), false, true, false, nil, `{"artifact_refs":["artifact:patch-1"],"checks":["Lease fencing"]}`)
	optionalEnum := func(values []string) map[string]any {
		return map[string]any{"enum": append(stringsAny(values), nil)}
	}
	add("comitia.finding.v1", validFinding, payloadObject(map[string]any{"severity": payloadEnum("info", "warning", "error"), "summary": payloadText(), "evidence_refs": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": []string{"string", "null"}}}, "priority": optionalEnum(findingPriorities), "size": optionalEnum(findingSizes)}, "severity", "summary"), false, false, false, []string{"Optional priority ranks urgency (P0 most urgent to P3); optional size estimates effort (S, M, L). Both are case-sensitive."}, `{"severity":"warning","summary":"A retry lost its result","evidence_refs":["artifact:trace-1"]}`, `{"severity":"error","summary":"Lease fencing is skipped on renew","priority":"P1","size":"S"}`)
	add(StanceSchema, validStance, payloadObject(map[string]any{"stance": payloadEnum(stances...), "note": nullablePayload(payloadText())}, "stance"), false, false, false, []string{"envelope.reply_to is required and must identify a message in this context (typically a finding or proposal); replay with reply_to or thread_id to tally stances.", "stance is agree, refine or disagree; optional note must be nonblank. Use refine with a note to amend rather than a new finding."}, `{"stance":"agree"}`, `{"stance":"refine","note":"Also cover the release path"}`)
	add("comitia.blocker.v1", validBlocker, payloadObject(map[string]any{"reason": payloadText(), "needs": payloadStrings()}, "reason", "needs"), false, true, false, nil, `{"reason":"Missing input","needs":["Expected recovery behavior"]}`)
	exactNotes := []string{"Without task_id, artifact_refs must identify exact artifacts (scheme:value, at most 512 bytes, no whitespace or control characters); git requires a full lowercase 40/64-character commit SHA; pr requires owner/repo#positive-number@full-SHA."}
	add("comitia.work-submitted.v1", validWorkSubmitted, payloadObject(map[string]any{"artifact_refs": payloadStrings(), "summary": payloadText()}, "artifact_refs", "summary"), false, false, true, exactNotes, `{"artifact_refs":["artifact:patch-1"],"summary":"Recovery implemented"}`)
	verification := payloadObject(map[string]any{"status": payloadEnum("passed", "failed", "not_run"), "checks": payloadStrings(), "evidence_refs": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": []string{"string", "null"}}}, "artifact_refs": nullablePayload(payloadStrings()), "reason": map[string]any{"type": []string{"string", "null"}}}, "status", "checks")
	verification["allOf"] = []any{
		map[string]any{"if": map[string]any{"properties": map[string]any{"status": map[string]any{"enum": []string{"passed", "failed"}}}}, "then": map[string]any{"required": []string{"evidence_refs"}, "properties": map[string]any{"evidence_refs": payloadStrings()}}},
		map[string]any{"if": map[string]any{"properties": map[string]any{"status": map[string]any{"const": "not_run"}}}, "then": map[string]any{"required": []string{"reason"}, "properties": map[string]any{"reason": payloadText()}}},
	}
	add("comitia.verification-result.v1", validVerificationResult, verification, false, false, true, exactNotes, `{"status":"passed","checks":["Recovery"],"evidence_refs":["artifact:test-log"],"artifact_refs":["artifact:patch-1"]}`, `{"status":"not_run","checks":["Integration"],"reason":"Runner unavailable","artifact_refs":["artifact:patch-1"]}`)
	approval := payloadObject(map[string]any{"artifact_refs": payloadStrings(), "verdict": payloadEnum("approved", "changes-requested"), "checks": payloadStrings(), "blocking_count": payloadInteger(0), "summary": map[string]any{"type": []string{"string", "null"}}}, "artifact_refs", "verdict", "checks", "blocking_count")
	approval["properties"].(map[string]any)["artifact_refs"].(map[string]any)["items"] = exactArtifactPayload()
	approval["allOf"] = []any{
		payloadCondition("verdict", map[string]any{"const": "approved"}, payloadFields(map[string]any{"blocking_count": map[string]any{"const": 0}})),
		payloadCondition("verdict", map[string]any{"const": "changes-requested"}, payloadFields(map[string]any{"blocking_count": payloadInteger(1)})),
	}
	add(ApprovalSchema, appendPayloadValidator(&ApprovalPayload{}, validApproval), approval, false, false, false, []string{"artifact_refs must always identify exact artifacts: scheme:value, at most 512 bytes, no whitespace/control characters; git pins a full lowercase 40/64-character SHA and pr pins owner/repo#positive-number@full-SHA.", "blocking_count must be zero for approved and positive for changes-requested."}, `{"artifact_refs":["artifact:patch-1"],"verdict":"approved","checks":["Recovery"],"blocking_count":0}`)
	bounded := func() map[string]any {
		return map[string]any{"type": []string{"array", "null"}, "maxItems": 64, "items": payloadText()}
	}
	add(TaskStatusSchema, appendPayloadValidator(&TaskStatusPayload{}, validTaskStatus), payloadObject(map[string]any{"state": payloadEnum("planned", "in_progress", "blocked", "review", "done", "failed", "cancelled"), "summary": payloadText(), "child_context_id": map[string]any{"type": []string{"string", "null"}}, "blockers": bounded(), "links": bounded()}, "state", "summary"), false, true, false, []string{"summary is at most 4096 bytes; child_context_id at most 128 bytes; blockers/links at most 64 entries, each nonblank and at most 1024 bytes.", "child_context_id, if nonempty, must be a direct child of the message context; naming it requires read permission on the message context."}, `{"state":"review","summary":"Ready for review","links":["artifact:patch-1"]}`)
	statuses := []any{"pending", "leased", "completed", "failed", "cancelled"}
	instance := payloadObject(map[string]any{"id": map[string]any{"type": "string", "pattern": "^[A-Za-z0-9._-]{1,64}$"}, "label": map[string]any{"type": []string{"string", "null"}}}, "id")
	add(TaskEventSchema, appendPayloadValidator(&TaskEventPayload{}, validTaskEvent), payloadObject(map[string]any{"kind": payloadEnum("created", "claimed", "renewed", "released", "completed", "failed", "cancelled"), "status": map[string]any{"enum": statuses}, "previous_status": map[string]any{"enum": append([]any{"", nil}, statuses...)}, "lease_version": map[string]any{"type": []string{"integer", "null"}, "minimum": 0, "maximum": int64(9223372036854775807)}, "lease_owner_session_id": map[string]any{"type": []string{"string", "null"}}, "lease_owner_instance": map[string]any{"anyOf": []any{map[string]any{"type": "null"}, instance}}, "reason": map[string]any{"type": []string{"string", "null"}}}, "kind", "status"), false, true, false, []string{"Server-authored: task mutations append these events atomically; append requests naming this schema are rejected. The descriptor documents replayed payloads.", "The message SessionID identifies the actor whose task mutation was authorized; lease_owner_instance is self-reported."}, `{"kind":"claimed","previous_status":"pending","status":"leased","lease_version":1,"lease_owner_session_id":"example-session-id","lease_owner_instance":{"id":"worker-1"}}`, `{"kind":"failed","previous_status":"leased","status":"failed","lease_version":1,"reason":"Tests failed"}`)
	criterion := payloadObject(map[string]any{"name": payloadText(), "outcome": payloadEnum("passed", "failed", "not_run"), "evidence_refs": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": []string{"string", "null"}}}, "reason": map[string]any{"type": []string{"string", "null"}}}, "name", "outcome")
	add(EvaluationResultSchema, appendPayloadValidator(&EvaluationResultPayload{}, validEvaluationResult), payloadObject(map[string]any{"evaluation_id": payloadText(), "outcome": payloadEnum("accepted", "rejected", "inconclusive"), "grader": payloadObject(map[string]any{"kind": payloadEnum("human", "agent", "automated"), "id": payloadText()}, "kind", "id"), "criteria": map[string]any{"type": "array", "minItems": 1, "items": criterion}, "cost": map[string]any{"anyOf": []any{map[string]any{"type": "null"}, payloadObject(map[string]any{"amount_micros": nullablePayload(payloadInteger(0)), "currency": map[string]any{"type": "string", "pattern": "^[A-Z]{3}$"}}, "currency")}}}, "evaluation_id", "outcome", "grader", "criteria"), false, true, false, []string{"Trimmed criterion names must be unique; passed/failed criteria need nonempty evidence_refs; not_run criteria need a nonblank reason.", "accepted requires all criteria passed; rejected requires a failed criterion; inconclusive requires not_run criteria and no failed criteria.", "cost.amount_micros defaults to zero when omitted and must fit int64."}, `{"evaluation_id":"evaluation-1","outcome":"accepted","grader":{"kind":"automated","id":"suite"},"criteria":[{"name":"Recovery","outcome":"passed","evidence_refs":["artifact:test-log"]}]}`)
	event := map[string]any{"run_id": payloadText(), "event_sequence": payloadInteger(1), "kind": payloadEnum("run_started", "model_usage", "tool_usage", "run_finished")}
	for _, name := range []string{"occurred_at", "provider", "model", "tool_name", "currency", "cost_source", "status"} {
		event[name] = map[string]any{"type": []string{"string", "null"}}
	}
	for _, name := range []string{"input_tokens", "output_tokens", "cached_input_tokens", "reasoning_tokens", "duration_ms", "cost_micros"} {
		event[name] = map[string]any{"type": []string{"integer", "null"}, "minimum": 0, "maximum": int64(9223372036854775807)}
	}
	event["dimensions"] = map[string]any{"type": []string{"object", "null"}, "maxProperties": 32, "propertyNames": payloadText(), "additionalProperties": payloadText()}
	optionalEmpty := map[string]any{"enum": []any{"", nil}}
	event["status"] = map[string]any{"enum": []any{"", nil, "succeeded", "failed", "cancelled"}}
	event["occurred_at"] = map[string]any{"type": []string{"string", "null"}}
	telemetry := payloadObject(event, "run_id", "event_sequence", "kind")
	var metrics []any
	for _, name := range []string{"input_tokens", "output_tokens", "cached_input_tokens", "reasoning_tokens", "duration_ms", "cost_micros"} {
		metrics = append(metrics, payloadFields(map[string]any{name: map[string]any{"type": "integer"}}, name))
	}
	telemetry["allOf"] = []any{
		payloadCondition("cost_micros", map[string]any{"type": "integer"}, payloadFields(map[string]any{"currency": map[string]any{"type": "string", "pattern": "^[A-Z]{3}$"}, "cost_source": payloadEnum("provider_reported", "estimated", "configured")}, "currency", "cost_source")),
		map[string]any{"if": map[string]any{"not": payloadFields(map[string]any{"cost_micros": map[string]any{"type": "integer"}}, "cost_micros")}, "then": payloadFields(map[string]any{"currency": optionalEmpty, "cost_source": optionalEmpty})},
		payloadCondition("kind", map[string]any{"const": "model_usage"}, map[string]any{"allOf": []any{payloadFields(map[string]any{"model": payloadText()}, "model"), map[string]any{"anyOf": metrics}}}),
		payloadCondition("kind", map[string]any{"const": "tool_usage"}, payloadFields(map[string]any{"tool_name": payloadText()}, "tool_name")),
		payloadCondition("kind", map[string]any{"const": "run_finished"}, payloadFields(map[string]any{"status": payloadEnum("succeeded", "failed", "cancelled")}, "status")),
	}
	add(ExecutionEventSchema, appendPayloadValidator(&executionEventPayload{}, validExecutionEvent), telemetry, false, true, false, []string{"event_sequence is positive and fits int64; optional metrics are nonnegative int64 values.", "Nonempty occurred_at must be RFC3339; nonempty status must be succeeded, failed or cancelled.", "cost_micros requires uppercase three-letter currency and cost_source provider_reported, estimated or configured; without cost_micros, currency/cost_source must be empty.", "model_usage requires nonblank model and at least one usage metric; tool_usage requires nonblank tool_name; run_finished requires status."}, `{"run_id":"run-1","event_sequence":1,"kind":"run_started"}`, `{"run_id":"run-1","event_sequence":2,"kind":"model_usage","model":"example-model","input_tokens":100}`)
	addCoordinationDescriptors(add)
	return out
}

func stringsAny(values []string) []any {
	out := make([]any, 0, len(values)+1)
	for _, v := range values {
		out = append(out, v)
	}
	return out
}

func exactArtifactPayload() map[string]any {
	sha := "([0-9a-f]{40}|[0-9a-f]{64})"
	return map[string]any{"type": "string", "maxLength": 512, "allOf": []any{
		map[string]any{"pattern": "^[a-z][a-z0-9+.-]*:[^" + payloadWhitespace + "\u0000-\u001f\u007f-\u009f]+$"},
		map[string]any{"if": map[string]any{"pattern": "^git:"}, "then": map[string]any{"pattern": "^git:" + sha + "$"}},
		map[string]any{"if": map[string]any{"pattern": "^pr:"}, "then": map[string]any{"pattern": "^pr:[^/#@]+/[^/#@]+#[1-9][0-9]*@" + sha + "$"}},
	}}
}

// builtinMessageSchemas returns the validator of every built-in schema in the registry.
func builtinMessageSchemas() map[string]PayloadValidator {
	out := map[string]PayloadValidator{}
	for id, schema := range builtinSchemas() {
		out[id] = schema.validate
	}
	return out
}
