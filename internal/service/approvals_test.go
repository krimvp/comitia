package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/krimvp/comitia/internal/model"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func TestApprovalAndArtifactMessagesWithoutTask(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	other, err := f.svc.CreateContext(ctx, f.admin.Token, model.CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := f.svc.CreateTask(ctx, f.admin.Token, other.ID, model.TaskInput{Title: "elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		env  model.MessageEnvelope
		want error
	}{
		{"approval", model.MessageEnvelope{Schema: model.ApprovalSchema, Payload: json.RawMessage(`{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":["go test"],"blocking_count":0}`)}, nil},
		{"invalid approval", model.MessageEnvelope{Schema: model.ApprovalSchema, Payload: json.RawMessage(`{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":["go test"],"blocking_count":3}`)}, model.ErrInvalid},
		{"work without task", model.MessageEnvelope{Schema: "comitia.work-submitted.v1", Payload: json.RawMessage(`{"artifact_refs":["git:` + sha + `"],"summary":"done"}`)}, nil},
		{"verification by artifact", model.MessageEnvelope{Schema: "comitia.verification-result.v1", Payload: json.RawMessage(`{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":["git:` + sha + `"]}`)}, nil},
		{"verification without task or artifact", model.MessageEnvelope{Schema: "comitia.verification-result.v1", Payload: json.RawMessage(`{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"]}`)}, model.ErrInvalid},
		{"verification with null artifacts", model.MessageEnvelope{Schema: "comitia.verification-result.v1", Payload: json.RawMessage(`{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":null}`)}, model.ErrInvalid},
		{"verification with empty artifacts", model.MessageEnvelope{Schema: "comitia.verification-result.v1", Payload: json.RawMessage(`{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":[]}`)}, model.ErrInvalid},
		{"foreign task still checked", model.MessageEnvelope{Schema: "comitia.work-submitted.v1", TaskID: foreign.ID, Payload: json.RawMessage(`{"artifact_refs":["git:` + sha + `"],"summary":"done"}`)}, model.ErrForbidden},
		{"assignment still needs task", model.MessageEnvelope{Schema: "comitia.assignment.v1", Payload: json.RawMessage(`{"instructions":"do","acceptance_criteria":["done"]}`)}, model.ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: tc.name, Envelope: &tc.env})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if err == nil && (m.Envelope == nil || m.Envelope.Schema != tc.env.Schema) {
				t.Fatalf("envelope not stored: %+v", m)
			}
		})
	}
}

func TestTasklessArtifactRefsMustBeExact(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, schema, task, payload string
		want                        error
	}{
		{"verification branch", "comitia.verification-result.v1", "", `{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":["main"]}`, model.ErrInvalid},
		{"verification short sha", "comitia.verification-result.v1", "", `{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":["git:abcdef1"]}`, model.ErrInvalid},
		{"verification opaque scheme", "comitia.verification-result.v1", "", `{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":["build:1842"]}`, nil},
		{"work branch", "comitia.work-submitted.v1", "", `{"artifact_refs":["main"],"summary":"done"}`, model.ErrInvalid},
		{"work uppercase git", "comitia.work-submitted.v1", "", `{"artifact_refs":["GIT:main"],"summary":"done"}`, model.ErrInvalid},
		{"work full sha", "comitia.work-submitted.v1", "", `{"artifact_refs":["git:` + sha + `"],"summary":"done"}`, nil},
		{"work with task keeps legacy refs", "comitia.work-submitted.v1", task.ID, `{"artifact_refs":["main"],"summary":"done"}`, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := model.MessageEnvelope{Schema: tc.schema, TaskID: tc.task, Payload: json.RawMessage(tc.payload)}
			if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: tc.name, Envelope: &env}); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}
