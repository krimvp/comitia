package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"

	"github.com/krimvp/comitia/internal/schema"
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
			err := model.UnknownField(tc.name, tc.fields)
			if !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), tc.want) || tc.avoid != "" && strings.Contains(err.Error(), tc.avoid) {
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
	err := schema.StrictAppendPayload(raw, &payload)
	if !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), `accepted fields: summary; did you mean "summary"?`) || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("nested payload: %v", err)
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	err = model.InvalidJSONFields(d.Decode(&payload), "payload", raw, &payload)
	if !strings.Contains(err.Error(), `accepted fields: summary; did you mean "summary"?`) || strings.Contains(err.Error(), "private-value") {
		t.Fatalf("nested JSON: %v", err)
	}
}

func TestSchemaValidationGuidance(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	_, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "schema", Envelope: &model.MessageEnvelope{Schema: "private-unknown-schema", Payload: json.RawMessage(`{}`)}})
	if !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "schema must name a registered message schema") || !strings.Contains(err.Error(), "list_message_schemas") || !strings.Contains(err.Error(), "comitia.discussion.v1") || strings.Contains(err.Error(), "private-unknown-schema") {
		t.Fatalf("schema: %v", err)
	}
	// Server-authored registered schemas remain valid replay filters.
	if _, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 10, model.MessageFilters{Schema: model.TaskEventSchema}); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceErrorsDoNotRevealTargets(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	other, err := f.svc.CreateContext(ctx, f.admin.Token, model.CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	foreign := messageWorker(t, f, "outsider", other.ID)
	task, err := f.svc.CreateTask(ctx, f.admin.Token, other.ID, model.TaskInput{Title: "private-task"})
	if err != nil {
		t.Fatal(err)
	}
	message, err := f.svc.AppendMessage(ctx, f.admin.Token, other.ID, model.MessageInput{DedupeKey: "foreign", Body: "private-message"})
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
				env := &model.MessageEnvelope{Schema: "comitia.discussion.v1", Payload: json.RawMessage(`{}`)}
				switch tc.field {
				case "task_id":
					env.TaskID = target
				case "reply_to":
					env.ReplyTo = target
				default:
					env.RecipientSessionIDs = []string{target}
				}
				_, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: fmt.Sprintf("%s-%d", tc.field, i), Body: "note", Envelope: env})
				info := model.SafeError("send_message", fmt.Errorf("private-wrapper: %w", err), model.NewRequestID())
				if !errors.Is(err, model.ErrForbidden) || !strings.Contains(info.Message, tc.field) || !strings.Contains(info.Message, tc.hint) || strings.Contains(info.Message, target) || strings.Contains(info.Message, "private-wrapper") {
					t.Fatalf("error: %v; info: %+v", err, info)
				}
				if previous != "" && previous != info.Message {
					t.Fatalf("existence leak: %q != %q", previous, info.Message)
				}
				previous = info.Message
				remote := &model.OperationalError{Info: info, Cause: model.ErrForbidden}
				if got := model.SafeError("send_message", remote, model.NewRequestID()); got.Message != info.Message {
					t.Fatalf("remote detail lost: %+v", got)
				}
			}
		})
	}
	// Arbitrary forbidden details remain private, including remote details.
	remote := &model.OperationalError{Info: model.ErrorInfo{Message: "forbidden: private-target", Hint: "private-hint"}, Cause: model.ErrForbidden}
	if info := model.SafeError("send_message", remote, model.NewRequestID()); info.Message != "forbidden" || info.Hint != "" {
		t.Fatalf("unsafe detail: %+v", info)
	}
}

func TestAllRepliesRequireEffectiveRead(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	root, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "root", Body: "private proposal"})
	if err != nil {
		t.Fatal(err)
	}
	_, writer, _, err := f.svc.DelegateSession(ctx, f.admin.Token, model.DelegateInput{PrincipalName: "write-only", TTL: time.Hour, Grants: []model.DelegatedGrant{{ContextID: f.context.ID, Permissions: model.PermissionWrite, ExpiresAt: f.now.Add(30 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "schema fixtures"})
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
				_, err := f.svc.AppendMessage(ctx, writer.Token, f.context.ID, model.MessageInput{DedupeKey: fmt.Sprintf("%s-%d", schema, i), Body: descriptor.Examples[0].Body, Envelope: &env})
				if !errors.Is(err, model.ErrForbidden) || !strings.Contains(err.Error(), "reply_to") {
					t.Fatalf("write-only reply: %v", err)
				}
				if previous != "" && previous != err.Error() {
					t.Fatalf("existence leak: %s != %s", previous, err)
				}
				previous = err.Error()
			}
		})
	}
	if _, err := f.svc.AppendMessage(ctx, writer.Token, f.context.ID, model.MessageInput{DedupeKey: "plain", Body: "ordinary write-only note"}); err != nil {
		t.Fatal(err)
	}
	child, err := f.svc.CreateChildContext(ctx, f.admin.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "readable child", ParentReadable: true})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := f.svc.AppendMessage(ctx, f.admin.Token, child.ID, model.MessageInput{DedupeKey: "proposal", Body: "proposal"})
	if err != nil {
		t.Fatal(err)
	}
	_, inherited, _, err := f.svc.DelegateSession(ctx, f.admin.Token, model.DelegateInput{PrincipalName: "inherited", TTL: time.Hour, Grants: []model.DelegatedGrant{{ContextID: f.context.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(30 * time.Minute)}, {ContextID: child.ID, Permissions: model.PermissionWrite, ExpiresAt: f.now.Add(30 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AppendMessage(ctx, inherited.Token, child.ID, model.MessageInput{DedupeKey: "inherited", Body: "reply", Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", ReplyTo: proposal.ID, Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatalf("inherited read: %v", err)
	}
}

func TestWriteOnlyReferencesDoNotRevealTasksOrSessions(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "private task"})
	if err != nil {
		t.Fatal(err)
	}
	_, writer, _, err := f.svc.DelegateSession(ctx, f.admin.Token, model.DelegateInput{PrincipalName: "writer", TTL: time.Hour, Grants: []model.DelegatedGrant{{ContextID: f.context.ID, Permissions: model.PermissionWrite, ExpiresAt: f.now.Add(30 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ field, existing string }{{"task_id", task.ID}, {"recipient_session_ids[0]", f.admin.Session.ID}} {
		var previous string
		for i, target := range []string{tc.existing, "missing-private-target"} {
			env := &model.MessageEnvelope{Schema: "comitia.discussion.v1", Payload: json.RawMessage(`{}`)}
			if tc.field == "task_id" {
				env.TaskID = target
			} else {
				env.RecipientSessionIDs = []string{target}
			}
			_, err := f.svc.AppendMessage(ctx, writer.Token, f.context.ID, model.MessageInput{DedupeKey: fmt.Sprintf("%s-%d", tc.field, i), Body: "note", Envelope: env})
			if !errors.Is(err, model.ErrForbidden) || !strings.Contains(err.Error(), tc.field) || strings.Contains(err.Error(), target) {
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
	svc, err := New(model.Config{Store: f.store, Clock: func() time.Time { return *f.now }, MessageSchemas: map[string]model.PayloadValidator{"example.custom.v1": func(json.RawMessage) error { return nil }}})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{"", "example.custom.v1", model.TaskEventSchema} {
		filters := model.MessageFilters{Schema: schema}
		if _, err := svc.ReplayMessages(context.Background(), f.admin.Token, f.context.ID, 0, 10, filters); err != nil {
			t.Fatalf("registered replay: %v", err)
		}
		if _, err := svc.WaitMessages(context.Background(), f.admin.Token, f.context.ID, 0, 0, filters); err != nil {
			t.Fatalf("registered wait: %v", err)
		}
	}
	if _, err := svc.ReplayMessages(context.Background(), f.admin.Token, f.context.ID, 0, 10, model.MessageFilters{Schema: "unknown"}); err != nil {
		t.Fatalf("unregistered filter: %v", err)
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
		err := model.InvalidJSONFields(d.Decode(&target), "arguments", raw, &target)
		if !strings.Contains(err.Error(), "accepted fields: beta") || strings.Contains(err.Error(), "alpha") || strings.Contains(err.Error(), "private-value") {
			t.Fatalf("wrong object contract: %v", err)
		}
	}
}

func TestRecipientRequestOrderAndCanonicalDedupe(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	reader := messageWorker(t, f, "reader", f.context.ID)
	recipients := []string{f.admin.Session.ID, reader.Session.ID}
	sort.Sort(sort.Reverse(sort.StringSlice(recipients)))
	original := append([]string(nil), recipients...)
	envelope := &model.MessageEnvelope{Schema: "comitia.discussion.v1", RecipientSessionIDs: recipients, Payload: json.RawMessage(`{}`)}
	first, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "canonical", Body: "note", Envelope: envelope})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(envelope.RecipientSessionIDs, original) {
		t.Fatalf("caller order changed: %v", envelope.RecipientSessionIDs)
	}
	sorted := append([]string(nil), original...)
	sort.Strings(sorted)
	if !reflect.DeepEqual(first.Envelope.RecipientSessionIDs, sorted) {
		t.Fatalf("stored order: %v", first.Envelope.RecipientSessionIDs)
	}
	retry, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "canonical", Body: "note", Envelope: &model.MessageEnvelope{Schema: envelope.Schema, RecipientSessionIDs: sorted, Payload: envelope.Payload}})
	if err != nil || retry.ID != first.ID {
		t.Fatalf("reordered retry: %+v %v", retry, err)
	}
	for _, tc := range []struct {
		recipients []string
		index      int
	}{
		{[]string{reader.Session.ID, "00000000000000000000000000000000"}, 1},
		{[]string{"00000000000000000000000000000000", reader.Session.ID}, 0},
	} {
		envelope.RecipientSessionIDs = tc.recipients
		_, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: fmt.Sprint("missing-", tc.index), Body: "note", Envelope: envelope})
		if !errors.Is(err, model.ErrForbidden) || !strings.Contains(err.Error(), fmt.Sprintf("recipient_session_ids[%d]", tc.index)) || strings.Contains(err.Error(), tc.recipients[1-tc.index]) || strings.Contains(err.Error(), "00000000000000000000000000000000") {
			t.Fatalf("wrong request index: %v", err)
		}
	}
}
