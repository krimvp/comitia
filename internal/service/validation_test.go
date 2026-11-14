package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/schema"
)

func TestValidationDetails(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	tests := []struct {
		name, detail string
		call         func() error
	}{
		{"title", "title required", func() error {
			_, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{})
			return err
		}},
		{"limit", "limit", func() error { _, err := f.svc.ListTasks(ctx, f.admin.Token, f.context.ID, "", 0); return err }},
		{"instance", "agent_instance.id", func() error {
			_, err := model.NormalizeAgentInstance(&model.AgentInstance{ID: "secret value"})
			return err
		}},
		{"resume instance", "agent_instance required", func() error {
			return f.svc.SetInstanceResume(ctx, f.admin.Token, f.context.ID, nil, json.RawMessage(`{}`))
		}},
		{"resume object", "resume must be a JSON object", func() error {
			return f.svc.SetInstanceResume(ctx, f.admin.Token, f.context.ID, &model.AgentInstance{ID: "worker"}, json.RawMessage(`["secret value"]`))
		}},
		{"presence instance", "agent_instance must be", func() error {
			id := "secret value"
			_, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 10, model.MessageFilters{AgentInstance: &id})
			return err
		}},
		{"unknown payload field", `unknown field "token"`, func() error {
			return schema.ValidTaskStatus(json.RawMessage(`{"state":"done","summary":"ready","token":"secret value"}`))
		}},
		{"payload field", "summary", func() error { return schema.ValidTaskStatus(json.RawMessage(`{"state":"done"}`)) }},
		{"payload type", "state", func() error { return schema.ValidTaskStatus(json.RawMessage(`{"state":42,"summary":"ready"}`)) }},
		{"task rule", "task_id required", func() error {
			_, err := f.svc.normalizeEnvelope(&model.MessageEnvelope{Schema: model.TaskStatusSchema, Payload: json.RawMessage(`{"state":"done","summary":"ready"}`)}, "")
			return err
		}},
		{"propagate payload detail", "summary", func() error {
			_, err := f.svc.normalizeEnvelope(&model.MessageEnvelope{Schema: model.TaskStatusSchema, Payload: json.RawMessage(`{"state":"done"}`)}, "")
			return err
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), tc.detail) || strings.Contains(err.Error(), "secret value") {
				t.Fatalf("detail: %v", err)
			}
		})
	}
}
