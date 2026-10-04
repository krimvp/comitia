package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestApprovalPayloadValidation(t *testing.T) {
	tests := []struct {
		name, payload string
		ok            bool
	}{
		{"approved commit", `{"artifact_refs":["git:0123abcd"],"verdict":"approved","checks":["go test -race ./..."],"blocking_count":0}`, true},
		{"approved pr at sha", `{"artifact_refs":["pr:krimvp/comitia#41@0123abcdef"],"verdict":"approved","checks":["review"],"blocking_count":0,"summary":"LGTM"}`, true},
		{"changes requested", `{"artifact_refs":["git:0123abcd"],"verdict":"changes-requested","checks":["review"],"blocking_count":2}`, true},
		{"other scheme", `{"artifact_refs":["sha256:feed"],"verdict":"approved","checks":["review"],"blocking_count":0}`, true},
		{"branch is not exact", `{"artifact_refs":["git:main"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"pr without sha", `{"artifact_refs":["pr:krimvp/comitia#41"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"no scheme", `{"artifact_refs":["0123abcd"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"whitespace", `{"artifact_refs":["git:0123abcd "],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"no refs", `{"artifact_refs":[],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"no checks", `{"artifact_refs":["git:0123abcd"],"verdict":"approved","checks":[],"blocking_count":0}`, false},
		{"missing blocking count", `{"artifact_refs":["git:0123abcd"],"verdict":"approved","checks":["review"]}`, false},
		{"negative blocking count", `{"artifact_refs":["git:0123abcd"],"verdict":"changes-requested","checks":["review"],"blocking_count":-1}`, false},
		{"fractional blocking count", `{"artifact_refs":["git:0123abcd"],"verdict":"changes-requested","checks":["review"],"blocking_count":1.5}`, false},
		{"approved with blockers", `{"artifact_refs":["git:0123abcd"],"verdict":"approved","checks":["review"],"blocking_count":1}`, false},
		{"changes without blockers", `{"artifact_refs":["git:0123abcd"],"verdict":"changes-requested","checks":["review"],"blocking_count":0}`, false},
		{"unknown verdict", `{"artifact_refs":["git:0123abcd"],"verdict":"approve","checks":["review"],"blocking_count":0}`, false},
		{"unknown field", `{"artifact_refs":["git:0123abcd"],"verdict":"approved","checks":["review"],"blocking_count":0,"merge":true}`, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := validApproval(json.RawMessage(tc.payload)); (err == nil) != tc.ok {
				t.Fatalf("got %v want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestApprovalAndArtifactMessagesWithoutTask(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	other, err := f.svc.CreateContext(ctx, f.admin.Token, CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := f.svc.CreateTask(ctx, f.admin.Token, other.ID, TaskInput{Title: "elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		env  MessageEnvelope
		want error
	}{
		{"approval", MessageEnvelope{Schema: ApprovalSchema, Payload: json.RawMessage(`{"artifact_refs":["git:0123abcd"],"verdict":"approved","checks":["go test"],"blocking_count":0}`)}, nil},
		{"invalid approval", MessageEnvelope{Schema: ApprovalSchema, Payload: json.RawMessage(`{"artifact_refs":["git:0123abcd"],"verdict":"approved","checks":["go test"],"blocking_count":3}`)}, ErrInvalid},
		{"work without task", MessageEnvelope{Schema: "comitia.work-submitted.v1", Payload: json.RawMessage(`{"artifact_refs":["git:0123abcd"],"summary":"done"}`)}, nil},
		{"verification by artifact", MessageEnvelope{Schema: "comitia.verification-result.v1", Payload: json.RawMessage(`{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":["git:0123abcd"]}`)}, nil},
		{"verification without task or artifact", MessageEnvelope{Schema: "comitia.verification-result.v1", Payload: json.RawMessage(`{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"]}`)}, ErrInvalid},
		{"verification with null artifacts", MessageEnvelope{Schema: "comitia.verification-result.v1", Payload: json.RawMessage(`{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":null}`)}, ErrInvalid},
		{"verification with empty artifacts", MessageEnvelope{Schema: "comitia.verification-result.v1", Payload: json.RawMessage(`{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":[]}`)}, ErrInvalid},
		{"foreign task still checked", MessageEnvelope{Schema: "comitia.work-submitted.v1", TaskID: foreign.ID, Payload: json.RawMessage(`{"artifact_refs":["git:0123abcd"],"summary":"done"}`)}, ErrForbidden},
		{"assignment still needs task", MessageEnvelope{Schema: "comitia.assignment.v1", Payload: json.RawMessage(`{"instructions":"do","acceptance_criteria":["done"]}`)}, ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: tc.name, Envelope: &tc.env})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if err == nil && (m.Envelope == nil || m.Envelope.Schema != tc.env.Schema) {
				t.Fatalf("envelope not stored: %+v", m)
			}
		})
	}
}
