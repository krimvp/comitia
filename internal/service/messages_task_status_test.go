package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"

	"github.com/krimvp/comitia/internal/schema"
)

func TestTaskStatusPayloadValidation(t *testing.T) {
	long := strings.Repeat("x", 4097)
	for raw, ok := range map[string]bool{
		`{"state":"planned","summary":"s"}`:                                             true,
		`{"state":"in_progress","summary":"s","blockers":["ci"],"links":["https://x"]}`: true,
		`{"state":"done","summary":"s","child_context_id":"c"}`:                         true,
		`{"state":"review","summary":"s"}`:                                              true,
		`{"state":"blocked","summary":"s","blockers":[]}`:                               true,
		`{"state":"started","summary":"s"}`:                                             false,
		`{"state":"done","summary":" "}`:                                                false,
		`{"state":"done"}`:                                                              false,
		`{"summary":"s"}`:                                                               false,
		`{"state":"done","summary":"s","extra":1}`:                                      false,
		`{"state":"done","summary":"s","blockers":[""]}`:                                false,
		`{"state":"done","summary":"s","links":[" "]}`:                                  false,
		`{"state":"done","summary":"s","links":"https://x"}`:                            false,
		`{"state":"done","summary":"` + long + `"}`:                                     false,
		`{"state":"done","summary":"s","child_context_id":"` + long[:129] + `"}`:        false,
	} {
		if err := schema.ValidTaskStatus(json.RawMessage(raw)); (err == nil) != ok {
			t.Errorf("%.80s: %v", raw, err)
		}
	}
	many := make([]string, 65)
	for i := range many {
		many[i] = "b"
	}
	raw, _ := json.Marshal(schema.TaskStatusPayload{State: "blocked", Summary: "s", Blockers: many})
	if schema.ValidTaskStatus(raw) == nil {
		t.Fatal("unbounded blockers accepted")
	}
}

func TestTaskStatusMessages(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "epic task"})
	if err != nil {
		t.Fatal(err)
	}
	coord, _ := delegate(t, f, f.admin.Token, "coord", f.context.ID, model.PermissionRead|model.PermissionWrite|model.PermissionCreate, time.Hour)
	reporter, _ := delegate(t, f, f.admin.Token, "reporter", f.context.ID, model.PermissionRead|model.PermissionWrite, time.Hour)
	child, err := f.svc.CreateChildContext(ctx, coord.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "task", TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	send := func(token, key, taskID, payload string) (model.Message, error) {
		return f.svc.AppendMessage(ctx, token, f.context.ID, model.MessageInput{DedupeKey: key, Envelope: &model.MessageEnvelope{Schema: model.TaskStatusSchema, TaskID: taskID, Payload: json.RawMessage(payload)}})
	}
	// The reporter cannot read the private child but may still reference it.
	m, err := send(reporter.Token, "status", task.ID, `{"state":"in_progress","summary":"halfway","child_context_id":"`+child.ID+`","links":["https://example.test/pr/1"]}`)
	if err != nil || m.Envelope.Schema != model.TaskStatusSchema {
		t.Fatalf("status: %+v %v", m, err)
	}
	if _, err := send(coord.Token, "plain", task.ID, `{"state":"done","summary":"ok"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := send(coord.Token, "notask", "", `{"state":"done","summary":"ok"}`); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("missing task_id: %v", err)
	}
	if _, err := send(coord.Token, "badtask", "missing", `{"state":"done","summary":"ok"}`); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("unknown task: %v", err)
	}
	// Grandchildren, siblings, the context itself and unknown IDs are not children of this context.
	grandchild, err := f.svc.CreateChildContext(ctx, coord.Token, model.CreateChildContextInput{ParentContextID: child.ID, Name: "nested"})
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := f.svc.CreateContext(ctx, f.admin.Token, model.CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "sibling"})
	if err != nil {
		t.Fatal(err)
	}
	for _, cid := range []string{grandchild.ID, sibling.ID, f.context.ID, "missing"} {
		if _, err := send(coord.Token, "child-"+cid, task.ID, `{"state":"done","summary":"ok","child_context_id":"`+cid+`"}`); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("child %s: %v", cid, err)
		}
	}
	// The task must belong to the context the status is sent in.
	if _, err := f.svc.AppendMessage(ctx, coord.Token, child.ID, model.MessageInput{DedupeKey: "nested", Envelope: &model.MessageEnvelope{Schema: model.TaskStatusSchema, TaskID: task.ID, Payload: json.RawMessage(`{"state":"done","summary":"ok"}`)}}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("task of another context: %v", err)
	}
}

// Write without read cannot probe task or child membership: references are refused alike.
func TestTaskStatusChildRequiresParentRead(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	coord, _ := delegate(t, f, f.admin.Token, "coord", f.context.ID, model.PermissionRead|model.PermissionWrite|model.PermissionCreate, time.Hour)
	child, err := f.svc.CreateChildContext(ctx, coord.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	writer, _ := delegate(t, f, f.admin.Token, "writer", f.context.ID, model.PermissionWrite, time.Hour)
	if _, err := f.svc.ListChildContexts(ctx, writer.Token, f.context.ID, "", 10); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("list children: %v", err)
	}
	task, err := f.svc.CreateTask(ctx, writer.Token, f.context.ID, model.TaskInput{Title: "probe"})
	if err != nil {
		t.Fatal(err)
	}
	send := func(key, payload string) error {
		_, err := f.svc.AppendMessage(ctx, writer.Token, f.context.ID, model.MessageInput{DedupeKey: key, Envelope: &model.MessageEnvelope{Schema: model.TaskStatusSchema, TaskID: task.ID, Payload: json.RawMessage(payload)}})
		return err
	}
	for _, cid := range []string{child.ID, "deadbeef", f.context.ID} {
		if err := send("probe-"+cid, `{"state":"done","summary":"s","child_context_id":"`+cid+`"}`); !errors.Is(err, model.ErrForbidden) || errors.Is(err, model.ErrInvalid) {
			t.Fatalf("probe %s: %v", cid, err)
		}
	}
	if err := send("plain", `{"state":"done","summary":"s"}`); !errors.Is(err, model.ErrForbidden) || !strings.Contains(err.Error(), "task_id") {
		t.Fatalf("write-only task reference without child: %v", err)
	}
}
