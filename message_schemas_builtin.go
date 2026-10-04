package comitia

import "encoding/json"

func payloadObject(properties map[string]any, required ...string) map[string]any {
	out := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func payloadText() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "pattern": `\S`}
}
func payloadStrings() map[string]any {
	return map[string]any{"type": "array", "minItems": 1, "items": payloadText()}
}
func payloadEnum(values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values}
}
func payloadInteger(min int) map[string]any { return map[string]any{"type": "integer", "minimum": min} }

func builtinMessageSchemaDescriptors() map[string]MessageSchemaDescriptor {
	out := map[string]MessageSchemaDescriptor{}
	add := func(id string, payload map[string]any, body, task, artifacts bool, requirements []string, examples ...string) {
		payload["$schema"] = "https://json-schema.org/draft/2020-12/schema"
		payload["$id"] = "urn:comitia:message-schema:" + id
		raw, _ := json.Marshal(payload)
		d := MessageSchemaDescriptor{ID: id, PayloadSchema: raw, BodyRequired: body, TaskRequired: task, TaskOrArtifactRefs: artifacts, Requirements: requirements, Examples: []MessageSchemaExample{}}
		if d.Requirements == nil {
			d.Requirements = []string{}
		}
		for _, example := range examples {
			e := MessageSchemaExample{Envelope: MessageEnvelope{Schema: id, Payload: json.RawMessage(example)}}
			if body {
				e.Body = "Discussion text"
			}
			if task {
				e.Envelope.TaskID = "example-task-id"
			}
			d.Examples = append(d.Examples, e)
		}
		out[id] = d
	}
	add("comitia.discussion.v1", payloadObject(map[string]any{}), true, false, false, nil, `{}`)
	add("comitia.assignment.v1", payloadObject(map[string]any{"instructions": payloadText(), "acceptance_criteria": payloadStrings()}, "instructions", "acceptance_criteria"), false, true, false, nil, `{"instructions":"Implement recovery","acceptance_criteria":["Retries are idempotent"]}`)
	add("comitia.review-request.v1", payloadObject(map[string]any{"artifact_refs": payloadStrings(), "checks": payloadStrings()}, "artifact_refs", "checks"), false, true, false, nil, `{"artifact_refs":["artifact:patch-1"],"checks":["Lease fencing"]}`)
	add("comitia.finding.v1", payloadObject(map[string]any{"severity": payloadEnum("info", "warning", "error"), "summary": payloadText(), "evidence_refs": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "string"}}}, "severity", "summary"), false, false, false, nil, `{"severity":"warning","summary":"A retry lost its result","evidence_refs":["artifact:trace-1"]}`)
	add("comitia.blocker.v1", payloadObject(map[string]any{"reason": payloadText(), "needs": payloadStrings()}, "reason", "needs"), false, true, false, nil, `{"reason":"Missing input","needs":["Expected recovery behavior"]}`)
	exactNotes := []string{"Without task_id, artifact_refs must identify exact artifacts (scheme:value, at most 512 bytes, no whitespace or control characters); git requires a full lowercase 40/64-character commit SHA; pr requires owner/repo#positive-number@full-SHA."}
	add("comitia.work-submitted.v1", payloadObject(map[string]any{"artifact_refs": payloadStrings(), "summary": payloadText()}, "artifact_refs", "summary"), false, false, true, exactNotes, `{"artifact_refs":["artifact:patch-1"],"summary":"Recovery implemented"}`)
	verification := payloadObject(map[string]any{"status": payloadEnum("passed", "failed", "not_run"), "checks": payloadStrings(), "evidence_refs": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "string"}}, "artifact_refs": payloadStrings(), "reason": map[string]any{"type": []string{"string", "null"}}}, "status", "checks")
	verification["allOf"] = []any{
		map[string]any{"if": map[string]any{"properties": map[string]any{"status": map[string]any{"enum": []string{"passed", "failed"}}}}, "then": map[string]any{"required": []string{"evidence_refs"}, "properties": map[string]any{"evidence_refs": payloadStrings()}}},
		map[string]any{"if": map[string]any{"properties": map[string]any{"status": map[string]any{"const": "not_run"}}}, "then": map[string]any{"required": []string{"reason"}, "properties": map[string]any{"reason": payloadText()}}},
	}
	add("comitia.verification-result.v1", verification, false, false, true, exactNotes, `{"status":"passed","checks":["Recovery"],"evidence_refs":["artifact:test-log"],"artifact_refs":["artifact:patch-1"]}`, `{"status":"not_run","checks":["Integration"],"reason":"Runner unavailable","artifact_refs":["artifact:patch-1"]}`)
	add(ApprovalSchema, payloadObject(map[string]any{"artifact_refs": payloadStrings(), "verdict": payloadEnum("approved", "changes-requested"), "checks": payloadStrings(), "blocking_count": payloadInteger(0), "summary": map[string]any{"type": []string{"string", "null"}}}, "artifact_refs", "verdict", "checks", "blocking_count"), false, false, false, []string{"artifact_refs must always identify exact artifacts: scheme:value, at most 512 bytes, no whitespace/control characters; git pins a full lowercase 40/64-character SHA and pr pins owner/repo#positive-number@full-SHA.", "blocking_count must be zero for approved and positive for changes-requested."}, `{"artifact_refs":["artifact:patch-1"],"verdict":"approved","checks":["Recovery"],"blocking_count":0}`)
	bounded := func() map[string]any {
		return map[string]any{"type": []string{"array", "null"}, "maxItems": 64, "items": payloadText()}
	}
	add(TaskStatusSchema, payloadObject(map[string]any{"state": payloadEnum("planned", "in_progress", "blocked", "review", "done"), "summary": payloadText(), "child_context_id": map[string]any{"type": []string{"string", "null"}}, "blockers": bounded(), "links": bounded()}, "state", "summary"), false, true, false, []string{"summary is at most 4096 bytes; child_context_id at most 128 bytes; blockers/links at most 64 entries, each nonblank and at most 1024 bytes.", "child_context_id, if nonempty, must be a direct child of the message context; naming it requires read permission on the message context."}, `{"state":"review","summary":"Ready for review","links":["artifact:patch-1"]}`)
	criterion := payloadObject(map[string]any{"name": payloadText(), "outcome": payloadEnum("passed", "failed", "not_run"), "evidence_refs": map[string]any{"type": []string{"array", "null"}, "items": map[string]any{"type": "string"}}, "reason": map[string]any{"type": []string{"string", "null"}}}, "name", "outcome")
	add(EvaluationResultSchema, payloadObject(map[string]any{"evaluation_id": payloadText(), "outcome": payloadEnum("accepted", "rejected", "inconclusive"), "grader": payloadObject(map[string]any{"kind": payloadEnum("human", "agent", "automated"), "id": payloadText()}, "kind", "id"), "criteria": map[string]any{"type": "array", "minItems": 1, "items": criterion}, "cost": map[string]any{"anyOf": []any{map[string]any{"type": "null"}, payloadObject(map[string]any{"amount_micros": payloadInteger(0), "currency": map[string]any{"type": "string", "pattern": "^[A-Z]{3}$"}}, "currency")}}}, "evaluation_id", "outcome", "grader", "criteria"), false, true, false, []string{"Trimmed criterion names must be unique; passed/failed criteria need nonempty evidence_refs; not_run criteria need a nonblank reason.", "accepted requires all criteria passed; rejected requires a failed criterion; inconclusive requires not_run criteria and no failed criteria.", "cost.amount_micros defaults to zero when omitted and must fit int64."}, `{"evaluation_id":"evaluation-1","outcome":"accepted","grader":{"kind":"automated","id":"suite"},"criteria":[{"name":"Recovery","outcome":"passed","evidence_refs":["artifact:test-log"]}]}`)
	event := map[string]any{"run_id": payloadText(), "event_sequence": payloadInteger(1), "kind": payloadEnum("run_started", "model_usage", "tool_usage", "run_finished")}
	for _, name := range []string{"occurred_at", "provider", "model", "tool_name", "currency", "cost_source", "status"} {
		event[name] = map[string]any{"type": []string{"string", "null"}}
	}
	for _, name := range []string{"input_tokens", "output_tokens", "cached_input_tokens", "reasoning_tokens", "duration_ms", "cost_micros"} {
		event[name] = map[string]any{"type": []string{"integer", "null"}, "minimum": 0, "maximum": int64(9223372036854775807)}
	}
	event["dimensions"] = map[string]any{"type": []string{"object", "null"}, "maxProperties": 32, "propertyNames": payloadText(), "additionalProperties": payloadText()}
	add(ExecutionEventSchema, payloadObject(event, "run_id", "event_sequence", "kind"), false, true, false, []string{"event_sequence is positive and fits int64; optional metrics are nonnegative int64 values.", "Nonempty occurred_at must be RFC3339; nonempty status must be succeeded, failed or cancelled.", "cost_micros requires uppercase three-letter currency and cost_source provider_reported, estimated or configured; without cost_micros, currency/cost_source must be empty.", "model_usage requires nonblank model and at least one usage metric; tool_usage requires nonblank tool_name; run_finished requires status."}, `{"run_id":"run-1","event_sequence":1,"kind":"run_started"}`, `{"run_id":"run-1","event_sequence":2,"kind":"model_usage","model":"example-model","input_tokens":100}`)
	return out
}
