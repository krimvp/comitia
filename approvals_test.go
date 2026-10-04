package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func TestApprovalPayloadValidation(t *testing.T) {
	tests := []struct {
		name, payload string
		ok            bool
	}{
		{"approved commit", `{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":["go test -race ./..."],"blocking_count":0}`, true},
		{"approved pr at sha", `{"artifact_refs":["pr:krimvp/comitia#41@` + sha + `"],"verdict":"approved","checks":["review"],"blocking_count":0,"summary":"LGTM"}`, true},
		{"changes requested", `{"artifact_refs":["git:` + sha + `"],"verdict":"changes-requested","checks":["review"],"blocking_count":2}`, true},
		{"other scheme", `{"artifact_refs":["sha256:feed"],"verdict":"approved","checks":["review"],"blocking_count":0}`, true},
		{"branch is not exact", `{"artifact_refs":["git:main"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"uppercase pr scheme", `{"artifact_refs":["PR:owner/repo#41"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"mixed-case git scheme", `{"artifact_refs":["Git:main"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"short sha", `{"artifact_refs":["git:abcdef1"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"pr without number", `{"artifact_refs":["pr:x@` + sha + `"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"pr without sha", `{"artifact_refs":["pr:krimvp/comitia#41"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"no scheme", `{"artifact_refs":["0123abcd"],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"whitespace", `{"artifact_refs":["git:` + sha + ` "],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"no refs", `{"artifact_refs":[],"verdict":"approved","checks":["review"],"blocking_count":0}`, false},
		{"no checks", `{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":[],"blocking_count":0}`, false},
		{"missing blocking count", `{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":["review"]}`, false},
		{"negative blocking count", `{"artifact_refs":["git:` + sha + `"],"verdict":"changes-requested","checks":["review"],"blocking_count":-1}`, false},
		{"fractional blocking count", `{"artifact_refs":["git:` + sha + `"],"verdict":"changes-requested","checks":["review"],"blocking_count":1.5}`, false},
		{"approved with blockers", `{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":["review"],"blocking_count":1}`, false},
		{"changes without blockers", `{"artifact_refs":["git:` + sha + `"],"verdict":"changes-requested","checks":["review"],"blocking_count":0}`, false},
		{"unknown verdict", `{"artifact_refs":["git:` + sha + `"],"verdict":"approve","checks":["review"],"blocking_count":0}`, false},
		{"unknown field", `{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":["review"],"blocking_count":0,"merge":true}`, false},
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
		{"approval", MessageEnvelope{Schema: ApprovalSchema, Payload: json.RawMessage(`{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":["go test"],"blocking_count":0}`)}, nil},
		{"invalid approval", MessageEnvelope{Schema: ApprovalSchema, Payload: json.RawMessage(`{"artifact_refs":["git:` + sha + `"],"verdict":"approved","checks":["go test"],"blocking_count":3}`)}, ErrInvalid},
		{"work without task", MessageEnvelope{Schema: "comitia.work-submitted.v1", Payload: json.RawMessage(`{"artifact_refs":["git:` + sha + `"],"summary":"done"}`)}, nil},
		{"verification by artifact", MessageEnvelope{Schema: "comitia.verification-result.v1", Payload: json.RawMessage(`{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":["git:` + sha + `"]}`)}, nil},
		{"verification without task or artifact", MessageEnvelope{Schema: "comitia.verification-result.v1", Payload: json.RawMessage(`{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"]}`)}, ErrInvalid},
		{"verification with null artifacts", MessageEnvelope{Schema: "comitia.verification-result.v1", Payload: json.RawMessage(`{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":null}`)}, ErrInvalid},
		{"verification with empty artifacts", MessageEnvelope{Schema: "comitia.verification-result.v1", Payload: json.RawMessage(`{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":[]}`)}, ErrInvalid},
		{"foreign task still checked", MessageEnvelope{Schema: "comitia.work-submitted.v1", TaskID: foreign.ID, Payload: json.RawMessage(`{"artifact_refs":["git:` + sha + `"],"summary":"done"}`)}, ErrForbidden},
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

func TestExactArtifactRef(t *testing.T) {
	sha256 := sha + "0123456789abcdef01234567"
	for ref, ok := range map[string]bool{
		"git:" + sha:                     true,
		"git:" + sha256:                  true,
		"pr:krimvp/comitia#41@" + sha:    true,
		"pr:krimvp/comitia#41@" + sha256: true,
		"sha256:feed":                    true,
		"ci+run.v2:1842":                 true,
		"git:abcdef1":                    false,
		"git:" + sha[:39]:                false,
		"git:0123456789ABCDEF0123456789ABCDEF01234567": false,
		"git:main":                    false,
		"GIT:main":                    false,
		"Git:main":                    false,
		"GIT:" + sha:                  false,
		"PR:owner/repo#41":            false,
		"Sha256:feed":                 false,
		"1git:" + sha:                 false,
		"pr:x@" + sha:                 false,
		"pr:x#@" + sha:                false,
		"pr:a#b#c@" + sha:             false,
		"pr:x#1@" + sha:               false,
		"pr:/repo#1@" + sha:           false,
		"pr:owner/#1@" + sha:          false,
		"pr:a/b/c#1@" + sha:           false,
		"pr:a/b#0@" + sha:             false,
		"pr:a/b#01@" + sha:            false,
		"pr:a/b#-1@" + sha:            false,
		"pr:a/b#1@abcdef1":            false,
		"pr:a/b#1@" + sha + "@" + sha: false,
		"pr:a/b#1":                    false,
		"main":                        false,
		":x":                          false,
		"git:":                        false,
	} {
		if exactArtifactRef(ref) != ok {
			t.Errorf("%q: want %v", ref, ok)
		}
	}
}

func TestTasklessArtifactRefsMustBeExact(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, schema, task, payload string
		want                        error
	}{
		{"verification branch", "comitia.verification-result.v1", "", `{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":["main"]}`, ErrInvalid},
		{"verification short sha", "comitia.verification-result.v1", "", `{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":["git:abcdef1"]}`, ErrInvalid},
		{"verification opaque scheme", "comitia.verification-result.v1", "", `{"status":"passed","checks":["go test"],"evidence_refs":["ci:1"],"artifact_refs":["build:1842"]}`, nil},
		{"work branch", "comitia.work-submitted.v1", "", `{"artifact_refs":["main"],"summary":"done"}`, ErrInvalid},
		{"work uppercase git", "comitia.work-submitted.v1", "", `{"artifact_refs":["GIT:main"],"summary":"done"}`, ErrInvalid},
		{"work full sha", "comitia.work-submitted.v1", "", `{"artifact_refs":["git:` + sha + `"],"summary":"done"}`, nil},
		{"work with task keeps legacy refs", "comitia.work-submitted.v1", task.ID, `{"artifact_refs":["main"],"summary":"done"}`, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := MessageEnvelope{Schema: tc.schema, TaskID: tc.task, Payload: json.RawMessage(tc.payload)}
			if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: tc.name, Envelope: &env}); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
}
