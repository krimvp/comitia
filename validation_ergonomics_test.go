package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestUnknownFieldGuidance(t *testing.T) {
	tests := []struct {
		name        string
		fields      []string
		want, avoid string
	}{
		{"after_sequence", []string{"limit", "after", "context_id"}, `did you mean "after"?`, ""},
		{"through", []string{"sequence", "context_id"}, `did you mean "sequence"?`, ""},
		{"summry", []string{"severity", "summary"}, `did you mean "summary"?`, ""},
		{"Summary", []string{"summary", "severity"}, `did you mean "summary"?`, ""},
		{"cat", []string{"bat", "hat"}, "accepted fields: bat, hat", "did you mean"},
		{"unrelated", []string{"after", "limit"}, "accepted fields: after, limit", "did you mean"},
		{"extra", nil, "no fields are accepted", "did you mean"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := UnknownField(tc.name, tc.fields)
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), tc.want) || tc.avoid != "" && strings.Contains(err.Error(), tc.avoid) {
				t.Fatalf("error: %v", err)
			}
		})
	}
	// Nested objects use their own contract, and never echo rejected values.
	var payload struct {
		Items []struct {
			Summary string `json:"summary"`
		} `json:"items"`
	}
	raw := json.RawMessage(`{"items":[{"summry":"private-value"}]}`)
	err := strictAppendPayload(raw, &payload)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), `accepted fields: summary; did you mean "summary"?`) || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("nested payload: %v", err)
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	err = InvalidJSONFields(d.Decode(&payload), "payload", raw, &payload)
	if !strings.Contains(err.Error(), `accepted fields: summary; did you mean "summary"?`) || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("nested JSON: %v", err)
	}
}

func TestSchemaValidationGuidance(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	for _, call := range []func() error{
		func() error {
			_, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "schema", Envelope: &MessageEnvelope{Schema: "private-unknown-schema", Payload: json.RawMessage(`{}`)}})
			return err
		},
		func() error {
			_, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 10, MessageFilters{Schema: "private-unknown-schema"})
			return err
		},
		func() error {
			_, err := f.svc.WaitMessages(ctx, f.admin.Token, f.context.ID, 0, time.Hour, MessageFilters{Schema: "private-unknown-schema"})
			return err
		},
	} {
		err := call()
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "schema must name a registered message schema") || !strings.Contains(err.Error(), "list_message_schemas") || !strings.Contains(err.Error(), "comitia.discussion.v1") || strings.Contains(err.Error(), "private-unknown-schema") {
			t.Fatalf("schema: %v", err)
		}
	}
	// Server-authored registered schemas remain valid replay filters.
	if _, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 10, MessageFilters{Schema: TaskEventSchema}); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceErrorsDoNotRevealTargets(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	other, err := f.svc.CreateContext(ctx, f.admin.Token, CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	foreign := messageWorker(t, f, "outsider", other.ID)
	task, err := f.svc.CreateTask(ctx, f.admin.Token, other.ID, TaskInput{Title: "private-task"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := f.svc.AppendMessage(ctx, f.admin.Token, other.ID, MessageInput{DedupeKey: "foreign", Body: "private-message"})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{ field, foreign, hint string }{
		{"task_id", task.ID, "list_tasks"}, {"reply_to", message.ID, "get_message"}, {"recipient_session_ids[0]", foreign.Session.ID, "list_participants"},
	}
	for _, tc := range tests {
		t.Run(tc.field, func(t *testing.T) {
			var previous string
			for i, target := range []string{tc.foreign, "missing-private-target"} {
				env := &MessageEnvelope{Schema: "comitia.discussion.v1", Payload: json.RawMessage(`{}`)}
				switch tc.field {
				case "task_id":
					env.TaskID = target
				case "reply_to":
					env.ReplyTo = target
				default:
					env.RecipientSessionIDs = []string{target}
				}
				_, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: fmt.Sprintf("%s-%d", tc.field, i), Body: "note", Envelope: env})
				info := SafeError("send_message", fmt.Errorf("private-wrapper: %w", err), NewRequestID())
				if !errors.Is(err, ErrForbidden) || !strings.Contains(info.Message, tc.field) || !strings.Contains(info.Message, tc.hint) || strings.Contains(info.Message, target) || strings.Contains(info.Message, "private-wrapper") {
					t.Fatalf("error: %v; info: %+v", err, info)
				}
				if previous != "" && previous != info.Message {
					t.Fatalf("existence leak: %q != %q", previous, info.Message)
				}
				previous = info.Message
				remote := &OperationalError{Info: info, Cause: ErrForbidden}
				if got := SafeError("send_message", remote, NewRequestID()); got.Message != info.Message {
					t.Fatalf("remote detail lost: %+v", got)
				}
			}
		})
	}
	// Arbitrary forbidden details remain private, including remote details.
	remote := &OperationalError{Info: ErrorInfo{Message: "forbidden: private-target", Hint: "private-hint"}, Cause: ErrForbidden}
	if info := SafeError("send_message", remote, NewRequestID()); info.Message != "forbidden" || info.Hint != "" {
		t.Fatalf("unsafe detail: %+v", info)
	}
}

func TestAllRepliesRequireEffectiveRead(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	root, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, MessageInput{DedupeKey: "root", Body: "private proposal"})
	if err != nil {
		t.Fatal(err)
	}
	_, writer, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{PrincipalName: "write-only", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionWrite, ExpiresAt: f.now.Add(30 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "schema fixtures"})
	if err != nil {
		t.Fatal(err)
	}
	for schema, descriptor := range f.svc.messageSchemaDescriptors {
		if descriptor.ServerAuthored {
			continue
		}
		t.Run(schema, func(t *testing.T) {
			env := descriptor.Examples[0].Envelope
			env.TaskID = task.ID
			var previous string
			for i, target := range []string{root.ID, "missing-private-target"} {
				env.ReplyTo = target
				_, err := f.svc.AppendMessage(ctx, writer.Token, f.context.ID, MessageInput{DedupeKey: fmt.Sprintf("%s-%d", schema, i), Body: descriptor.Examples[0].Body, Envelope: &env})
				if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), "reply_to") {
					t.Fatalf("write-only reply: %v", err)
				}
				if previous != "" && previous != err.Error() {
					t.Fatalf("existence leak: %s != %s", previous, err)
				}
				previous = err.Error()
			}
		})
	}
	if _, err := f.svc.AppendMessage(ctx, writer.Token, f.context.ID, MessageInput{DedupeKey: "plain", Body: "ordinary write-only note"}); err != nil {
		t.Fatal(err)
	}
	child, err := f.svc.CreateChildContext(ctx, f.admin.Token, CreateChildContextInput{ParentContextID: f.context.ID, Name: "readable child", ParentReadable: true})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := f.svc.AppendMessage(ctx, f.admin.Token, child.ID, MessageInput{DedupeKey: "proposal", Body: "proposal"})
	if err != nil {
		t.Fatal(err)
	}
	_, inherited, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{PrincipalName: "inherited", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(30 * time.Minute)}, {ContextID: child.ID, Permissions: PermissionWrite, ExpiresAt: f.now.Add(30 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AppendMessage(ctx, inherited.Token, child.ID, MessageInput{DedupeKey: "inherited", Body: "reply", Envelope: &MessageEnvelope{Schema: "comitia.discussion.v1", ReplyTo: proposal.ID, Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatalf("inherited read: %v", err)
	}
}

func TestWriteOnlyReferencesDoNotRevealTasksOrSessions(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "private task"})
	if err != nil {
		t.Fatal(err)
	}
	_, writer, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{PrincipalName: "writer", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionWrite, ExpiresAt: f.now.Add(30 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ field, existing string }{{"task_id", task.ID}, {"recipient_session_ids[0]", f.admin.Session.ID}} {
		var previous string
		for i, target := range []string{tc.existing, "missing-private-target"} {
			env := &MessageEnvelope{Schema: "comitia.discussion.v1", Payload: json.RawMessage(`{}`)}
			if tc.field == "task_id" {
				env.TaskID = target
			} else {
				env.RecipientSessionIDs = []string{target}
			}
			_, err := f.svc.AppendMessage(ctx, writer.Token, f.context.ID, MessageInput{DedupeKey: fmt.Sprintf("%s-%d", tc.field, i), Body: "note", Envelope: env})
			if !errors.Is(err, ErrForbidden) || !strings.Contains(err.Error(), tc.field) || strings.Contains(err.Error(), target) {
				t.Fatalf("write-only reference: %v", err)
			}
			if previous != "" && previous != err.Error() {
				t.Fatalf("existence leak: %q != %q", previous, err)
			}
			previous = err.Error()
		}
	}
}

func TestRegisteredExtensionSchemaFilters(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	svc, err := New(Config{Store: f.store, Clock: func() time.Time { return *f.now }, MessageSchemas: map[string]PayloadValidator{"example.custom.v1": func(json.RawMessage) error { return nil }}})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"", "example.custom.v1", TaskEventSchema} {
		filters := MessageFilters{Schema: schema}
		if _, err := svc.ReplayMessages(context.Background(), f.admin.Token, f.context.ID, 0, 10, filters); err != nil {
			t.Fatalf("registered replay: %v", err)
		}
		if _, err := svc.WaitMessages(context.Background(), f.admin.Token, f.context.ID, 0, 0, filters); err != nil {
			t.Fatalf("registered wait: %v", err)
		}
	}
	_, err = svc.ReplayMessages(context.Background(), f.admin.Token, f.context.ID, 0, 10, MessageFilters{Schema: "unknown"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "example.custom.v1") {
		t.Fatalf("extension discovery: %v", err)
	}
}

func TestUnknownFieldGuidanceUsesFirstFailingObject(t *testing.T) {
	var target struct {
		First struct {
			Alpha string `json:"alpha"`
		} `json:"first"`
		Second struct {
			Beta string `json:"beta"`
		} `json:"second"`
	}
	// Identically named unknown keys must use the first decoder failure's
	// contract, regardless of map iteration order or large numeric values.
	raw := json.RawMessage(`{"second":{"unexpected":1e1000},"first":{"unexpected":"private-value"}}`)
	for i := 0; i < 20; i++ {
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.DisallowUnknownFields()
		err := InvalidJSONFields(d.Decode(&target), "arguments", raw, &target)
		if !strings.Contains(err.Error(), "accepted fields: beta") || strings.Contains(err.Error(), "alpha") || strings.Contains(err.Error(), "private-value") {
			t.Fatalf("wrong object contract: %v", err)
		}
	}
}
