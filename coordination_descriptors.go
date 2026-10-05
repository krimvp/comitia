package comitia

import "strings"

func addCoordinationDescriptors(add func(string, map[string]any, bool, bool, bool, []string, ...string)) {
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
	add(RequestSchema, payloadObject(map[string]any{"operation": payloadText(), "instructions": payloadText(), "expected_responders": ids(), "deadline": payloadText(), "artifact_refs": refs()}, "operation", "instructions", "expected_responders", "deadline"), false, false, false,
		[]string{"deadline is an absolute RFC3339 timestamp in the future at first append. Expected responders must be active readable authenticated sessions in this context; IDs are unique, at most 64. Control operations execute externally; instructions must contain no secrets."},
		`{"operation":"control.pause","instructions":"Pause at the next checkpoint","expected_responders":["example-session"],"deadline":"2099-01-01T00:00:00Z"}`)
	receipt := payloadObject(map[string]any{"status": payloadEnum("accepted", "completed", "rejected"), "result_refs": refs(), "reason": text()}, "status")
	receipt["allOf"] = []any{
		payloadCondition("status", map[string]any{"const": "completed"}, payloadFields(map[string]any{"result_refs": map[string]any{"type": "array", "minItems": 1, "items": exactArtifactPayload()}}, "result_refs")),
		payloadCondition("status", map[string]any{"const": "rejected"}, payloadFields(map[string]any{"reason": payloadText()}, "reason")),
	}
	add(ReceiptSchema, receipt, false, false, false,
		[]string{"envelope.reply_to must identify a request in this context; task_id must match. Authenticated sender must be an expected responder. New receipts at or after the deadline are rejected; identical dedupe retries remain recoverable. accepted means accepted for handling, not completed; completed requires durable result_refs."},
		`{"status":"accepted"}`, `{"status":"completed","result_refs":["checkpoint:launch-1/paused"]}`, `{"status":"rejected","reason":"Unsupported control"}`)
	certificate := payloadObject(map[string]any{"policy_id": payloadText(), "artifact_ref": exactArtifactPayload(), "head_sha": shaField(), "approval_ids": ids(), "issued_at": payloadText(), "expires_at": payloadText(), "consumption_key": payloadText()}, "policy_id", "artifact_ref", "head_sha", "approval_ids", "issued_at", "expires_at", "consumption_key")
	add(MergeCertificateSchema, certificate, false, false, false,
		[]string{"artifact_ref is an exact pr reference matching full lowercase head_sha. issued_at and expires_at are RFC3339 and expiry follows issuance. reply_to identifies a request; approval_ids identify approval.v1 messages replying to that request in this context with matching task_id. This schema is shape/correlation only; the external gate must verify authenticated roles, current SHA, freshness and invalidation, reserve consumption once and reconcile GitHub."},
		`{"policy_id":"merge-policy-1","artifact_ref":"`+artifact+`","head_sha":"`+sha+`","approval_ids":["example-approval"],"issued_at":"2026-01-01T00:00:00Z","expires_at":"2099-01-01T00:00:00Z","consumption_key":"merge-operation-1"}`)
	merge := payloadObject(map[string]any{"operation_id": payloadText(), "status": payloadEnum("merged", "not_merged"), "head_sha": shaField(), "merge_sha": text(), "evidence_refs": map[string]any{"type": "array", "minItems": 1, "items": exactArtifactPayload()}}, "operation_id", "status", "head_sha", "evidence_refs")
	merge["allOf"] = []any{
		payloadCondition("status", map[string]any{"const": "merged"}, payloadFields(map[string]any{"merge_sha": shaField()}, "merge_sha")),
		payloadCondition("status", map[string]any{"const": "not_merged"}, payloadFields(map[string]any{"merge_sha": map[string]any{"enum": []any{"", nil}}})),
	}
	add(MergeReceiptSchema, merge, false, false, false,
		[]string{"reply_to identifies a certificate; task_id, operation_id (certificate consumption_key) and head_sha must match. merged requires a full lowercase merge_sha; not_merged requires empty merge_sha. Evidence identifies external reconciliation; sender must be checked against launcher-owned merger roles, and a receipt is not proof of GitHub state."},
		`{"operation_id":"merge-operation-1","status":"merged","head_sha":"`+sha+`","merge_sha":"`+sha+`","evidence_refs":["github:merge-operation-1"]}`)
}
