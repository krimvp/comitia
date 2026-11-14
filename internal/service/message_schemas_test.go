package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"

	"github.com/krimvp/comitia/internal/schema"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestMessageSchemaDiscoveryContracts(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	descriptors, err := f.svc.ListMessageSchemas(ctx, f.admin.Token)
	if err != nil || len(descriptors) != len(schema.BuiltinMessageSchemas()) {
		t.Fatalf("list: %v %v", descriptors, err)
	}
	var ids []string
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "examples"})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range descriptors {
		ids = append(ids, d.ID)
		got, err := f.svc.GetMessageSchema(ctx, f.admin.Token, d.ID)
		if err != nil || !reflect.DeepEqual(got, d) {
			t.Fatalf("get %s: %v", d.ID, err)
		}
		if d.Opaque || len(d.Examples) == 0 || d.Requirements == nil {
			t.Fatalf("incomplete descriptor: %+v", d)
		}
		var schema any
		if err := json.Unmarshal(d.PayloadSchema, &schema); err != nil {
			t.Fatal(err)
		}
		compiler := jsonschema.NewCompiler()
		compiler.UseLoader(jsonschema.SchemeURLLoader{})
		url := "urn:comitia:message-schema:" + d.ID
		if err := compiler.AddResource(url, schema); err != nil {
			t.Fatal(err)
		}
		compiled, err := compiler.Compile(url)
		if err != nil {
			t.Fatal(err)
		}
		for i, ex := range d.Examples {
			var payload any
			if err := json.Unmarshal(ex.Envelope.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if err := compiled.Validate(payload); err != nil {
				t.Fatalf("schema example %s: %v", d.ID, err)
			}
			if ex.Envelope.TaskID != "" {
				ex.Envelope.TaskID = task.ID
			}
			ex.Envelope = materializeProtocolExample(t, f, d.ID+string(rune('0'+i)), ex.Envelope)
			_, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: d.ID + string(rune('0'+i)), Body: ex.Body, Envelope: &ex.Envelope})
			if d.ServerAuthored != (d.ID == model.TaskEventSchema) || d.ServerAuthored && !errors.Is(err, model.ErrInvalid) {
				t.Fatalf("server-authored example %s: %v", d.ID, err)
			}
			if err != nil && !d.ServerAuthored {
				t.Fatalf("append example %s: %v", d.ID, err)
			}
		}
	}
	if !sort.StringsAreSorted(ids) {
		t.Fatalf("unstable discovery order: %v", ids)
	}
	descriptors[0].PayloadSchema[0] = 'x'
	descriptors[0].Requirements = append(descriptors[0].Requirements, "mutated")
	descriptors[0].Examples[0].Envelope.Payload[0] = 'x'
	again, err := f.svc.ListMessageSchemas(ctx, f.admin.Token)
	if err != nil || !json.Valid(again[0].PayloadSchema) || !json.Valid(again[0].Examples[0].Envelope.Payload) {
		t.Fatalf("mutable registry: %v", err)
	}
	for _, token := range []string{"invalid", f.admin.Token} {
		_, err := f.svc.GetMessageSchema(ctx, token, "missing.v1")
		want := model.ErrNotFound
		if token == "invalid" {
			want = model.ErrUnauthenticated
		}
		if !errors.Is(err, want) {
			t.Fatalf("get error: %v", err)
		}
	}
	if _, err := f.svc.ListMessageSchemas(ctx, "invalid"); !errors.Is(err, model.ErrUnauthenticated) {
		t.Fatalf("list auth: %v", err)
	}
	// Discovery is independent of grants and never reveals contexts or messages.
	principal, err := f.svc.CreatePrincipal(ctx, f.admin.Token, "no grants")
	if err != nil {
		t.Fatal(err)
	}
	ungranted, err := f.svc.MintSession(ctx, f.admin.Token, principal.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ListMessageSchemas(ctx, ungranted.Token); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RevokeSession(ctx, f.admin.Token, ungranted.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ListMessageSchemas(ctx, ungranted.Token); !errors.Is(err, model.ErrUnauthenticated) {
		t.Fatalf("revoked discovery: %v", err)
	}
}

func TestExtensionMessageSchemaDescriptors(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	id := "project.metric.v1"
	descriptor := model.MessageSchemaDescriptor{BodyRequired: true, TaskRequired: true, PayloadSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"value":{"type":"integer","minimum":1}},"required":["value"],"additionalProperties":false}`), Requirements: []string{"value must be positive"}, Examples: []model.MessageSchemaExample{{Body: "Metric", Envelope: model.MessageEnvelope{Schema: id, TaskID: "example-task-id", Payload: json.RawMessage(`{"value":1}`)}}}}
	validator := func(raw json.RawMessage) error {
		var p struct {
			Value int `json:"value"`
		}
		if err := schema.StrictPayload(raw, &p); err != nil {
			return err
		}
		if p.Value < 1 {
			return model.ErrInvalid
		}
		return nil
	}
	cfg := model.Config{Store: f.store, Clock: func() time.Time { return *f.now }, MessageSchemas: map[string]model.PayloadValidator{id: validator, "project.opaque.v1": validator}, MessageSchemaDescriptors: map[string]model.MessageSchemaDescriptor{id: descriptor}}
	svc, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	got, err := svc.GetMessageSchema(ctx, f.admin.Token, id)
	if err != nil || got.ID != id || got.Opaque || !got.TaskRequired || !got.BodyRequired || len(got.Examples) != 1 {
		t.Fatalf("extension descriptor: %+v %v", got, err)
	}
	list, err := svc.ListMessageSchemas(ctx, f.admin.Token)
	if err != nil || len(list) != len(schema.BuiltinMessageSchemas())+2 {
		t.Fatalf("extension list: %v %v", list, err)
	}
	opaque, err := svc.GetMessageSchema(ctx, f.admin.Token, "project.opaque.v1")
	if err != nil || !opaque.Opaque || len(opaque.Examples) != 0 {
		t.Fatalf("opaque: %+v %v", opaque, err)
	}
	descriptor.PayloadSchema[0] = 'x'
	descriptor.Requirements[0] = "mutated"
	descriptor.Examples[0].Envelope.Payload[0] = 'x'
	got, err = svc.GetMessageSchema(ctx, f.admin.Token, id)
	if err != nil || !json.Valid(got.PayloadSchema) || got.Requirements[0] != "value must be positive" || !json.Valid(got.Examples[0].Envelope.Payload) {
		t.Fatalf("config aliases registry: %+v %v", got, err)
	}
	// Declared envelope requirements are enforced for described extensions.
	env := got.Examples[0].Envelope
	for _, in := range []model.MessageInput{{Envelope: &env}, {Body: "Metric", Envelope: &model.MessageEnvelope{Schema: id, Payload: json.RawMessage(`{"value":1}`)}}} {
		if _, err := svc.normalizeEnvelope(in.Envelope, in.Body); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("extension requirement: %v", err)
		}
	}
	// Legacy validators continue to accept messages without task/body requirements.
	if _, err := svc.normalizeEnvelope(&model.MessageEnvelope{Schema: "project.opaque.v1", Payload: json.RawMessage(`{"value":1}`)}, ""); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		edit func(*model.MessageSchemaDescriptor)
	}{
		{"mismatched id", func(d *model.MessageSchemaDescriptor) { d.ID = "project.other.v1" }},
		{"invalid JSON", func(d *model.MessageSchemaDescriptor) { d.PayloadSchema = json.RawMessage(`{`) }},
		{"invalid metaschema", func(d *model.MessageSchemaDescriptor) {
			d.PayloadSchema = json.RawMessage(`{"type":"object","required":1}`)
		}},
		{"external ref", func(d *model.MessageSchemaDescriptor) {
			d.PayloadSchema = json.RawMessage(`{"type":"object","$ref":"https://example.invalid/schema"}`)
		}},
		{"nonobject", func(d *model.MessageSchemaDescriptor) { d.PayloadSchema = json.RawMessage(`{"type":"array"}`) }},
		{"no examples", func(d *model.MessageSchemaDescriptor) { d.Examples = nil }},
		{"wrong example schema", func(d *model.MessageSchemaDescriptor) { d.Examples[0].Envelope.Schema = "project.other.v1" }},
		{"invalid schema example", func(d *model.MessageSchemaDescriptor) {
			d.Examples[0].Envelope.Payload = json.RawMessage(`{"value":0}`)
		}},
		{"invalid validator example", func(d *model.MessageSchemaDescriptor) {
			d.PayloadSchema = json.RawMessage(`{"type":"object"}`)
			d.Examples[0].Envelope.Payload = json.RawMessage(`{"value":0}`)
		}},
		{"missing body", func(d *model.MessageSchemaDescriptor) { d.Examples[0].Body = "" }},
		{"missing task", func(d *model.MessageSchemaDescriptor) { d.Examples[0].Envelope.TaskID = "" }},
		{"opaque descriptor", func(d *model.MessageSchemaDescriptor) { d.Opaque = true }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := schema.CloneMessageSchema(got)
			tc.edit(&d)
			cfg.MessageSchemaDescriptors = map[string]model.MessageSchemaDescriptor{id: d}
			if _, err := New(cfg); !errors.Is(err, model.ErrInvalid) {
				t.Fatalf("accepted invalid descriptor: %v", err)
			}
		})
	}
	for _, id := range []string{"comitia.discussion.v1", "unregistered.v1"} {
		cfg.MessageSchemaDescriptors = map[string]model.MessageSchemaDescriptor{id: got}
		if _, err := New(cfg); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("invalid registration: %v", err)
		}
	}
}

func TestReviewPanelFindingPriorityAndStance(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	other, err := f.svc.CreateContext(ctx, f.admin.Token, model.CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	local := messageWorker(t, f, "reviewer", f.context.ID)
	outsider := messageWorker(t, f, "outsider", other.ID)
	foreign, err := f.svc.AppendMessage(ctx, outsider.Token, other.ID, model.MessageInput{DedupeKey: "private", Body: "private"})
	if err != nil {
		t.Fatal(err)
	}
	send := func(key, schema, reply, payload string) (model.Message, error) {
		return f.svc.AppendMessage(ctx, local.Token, f.context.ID, model.MessageInput{DedupeKey: key, Envelope: &model.MessageEnvelope{Schema: schema, ReplyTo: reply, Payload: json.RawMessage(payload)}})
	}
	// Findings without priority/size stay valid; both new fields are optional.
	if _, err := send("legacy", "comitia.finding.v1", "", `{"severity":"warning","summary":"old shape"}`); err != nil {
		t.Fatal(err)
	}
	finding, err := send("ranked", "comitia.finding.v1", "", `{"severity":"error","summary":"lease skipped","priority":"P1","size":"S"}`)
	if err != nil || string(finding.Envelope.Payload) != `{"priority":"P1","severity":"error","size":"S","summary":"lease skipped"}` {
		t.Fatalf("ranked finding: %s %v", finding.Envelope.Payload, err)
	}
	for _, payload := range []string{`{"severity":"info","summary":"s","priority":"P4"}`, `{"severity":"info","summary":"s","size":"XL"}`, `{"severity":"info","summary":"s","priority":""}`} {
		if _, err := send("bad-"+payload, "comitia.finding.v1", "", payload); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("finding %s: %v", payload, err)
		}
	}
	for i, stance := range []string{`{"stance":"agree"}`, `{"stance":"refine","note":"cover release too"}`, `{"stance":"disagree","note":"not a bug"}`} {
		if _, err := send(fmt.Sprint("stance-", i), model.StanceSchema, finding.ID, stance); err != nil {
			t.Fatalf("stance %s: %v", stance, err)
		}
	}
	tests := []struct {
		name, reply, payload string
		want                 error
	}{
		{"missing reply_to", "", `{"stance":"agree"}`, model.ErrInvalid},
		{"unknown stance", finding.ID, `{"stance":"abstain"}`, model.ErrInvalid},
		{"blank note", finding.ID, `{"stance":"agree","note":" "}`, model.ErrInvalid},
		// Foreign and missing roots fail identically, so existence is not revealed.
		{"foreign reply_to", foreign.ID, `{"stance":"agree"}`, model.ErrForbidden},
		{"missing reply_to target", "no-such-message", `{"stance":"agree"}`, model.ErrForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := send(tc.name, model.StanceSchema, tc.reply, tc.payload); !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	replies, err := f.svc.ReplayMessages(ctx, local.Token, f.context.ID, 0, 100, model.MessageFilters{ReplyTo: finding.ID, Schema: model.StanceSchema})
	if err != nil || len(replies) != 3 {
		t.Fatalf("stance tally: %d %v", len(replies), err)
	}
	if _, err := f.svc.GetMessageSchema(ctx, local.Token, model.StanceSchema); err != nil {
		t.Fatalf("stance discovery: %v", err)
	}
}

func TestStanceWriteOnlyDoesNotRevealMessageExistence(t *testing.T) {
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
	if _, err := f.svc.GetMessage(ctx, writer.Token, f.context.ID, root.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("fixture must deny message read: %v", err)
	}
	stance := func(token, cid, key, target string) error {
		_, err := f.svc.AppendMessage(ctx, token, cid, model.MessageInput{DedupeKey: key, Envelope: &model.MessageEnvelope{Schema: model.StanceSchema, ReplyTo: target, Payload: json.RawMessage(`{"stance":"agree"}`)}})
		return err
	}
	for i, target := range []string{root.ID, "no-such-message"} {
		if err := stance(writer.Token, f.context.ID, fmt.Sprint("stance-", i), target); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("write-only stance existing=%v: %v", i == 0, err)
		}
	}
	// Ordinary write-only messages are unaffected.
	if _, err := f.svc.AppendMessage(ctx, writer.Token, f.context.ID, model.MessageInput{DedupeKey: "plain", Body: "note"}); err != nil {
		t.Fatal(err)
	}
	// Read inherited from a parent satisfies the check with write on the child.
	child, err := f.svc.CreateChildContext(ctx, f.admin.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "panel", ParentReadable: true})
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
	if err := stance(inherited.Token, child.ID, "inherited", proposal.ID); err != nil {
		t.Fatalf("inherited read stance: %v", err)
	}
}
