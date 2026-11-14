package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

type messageReadProbe struct {
	comitia.API
	token, cid string
	after      int64
	limit      int
	timeout    time.Duration
	filters    []comitia.MessageFilters
}

func (p *messageReadProbe) ReplayMessages(_ context.Context, token, cid string, after int64, limit int, filters ...comitia.MessageFilters) ([]comitia.Message, error) {
	p.token, p.cid, p.after, p.limit, p.filters = token, cid, after, limit, filters
	return []comitia.Message{}, nil
}
func (p *messageReadProbe) WaitMessages(_ context.Context, token, cid string, after int64, timeout time.Duration, filters ...comitia.MessageFilters) ([]comitia.Message, error) {
	p.token, p.cid, p.after, p.timeout, p.filters = token, cid, after, timeout, filters
	return []comitia.Message{}, nil
}
func TestMCPMessageReadFiltersAndTimeout(t *testing.T) {
	probe := new(messageReadProbe)
	s := &Server{API: probe, Credential: "configured"}
	raw := json.RawMessage(`{"context_id":"context","after":42,"for_instance":"reader","from_instance":"writer","task_id":"task","schema":"comitia.discussion.v1"}`)
	want := []comitia.MessageFilters{{ForInstance: "reader", FromInstance: "writer", TaskID: "task", Schema: "comitia.discussion.v1"}}
	for _, tool := range []string{"replay_messages", "wait_messages"} {
		v, err := s.call(context.Background(), tool, raw)
		if err != nil || len(v.([]comitia.Message)) != 0 || probe.token != "configured" || probe.cid != "context" || probe.after != 42 || !reflect.DeepEqual(probe.filters, want) {
			t.Fatalf("%s: %+v %v %v", tool, probe, v, err)
		}
	}
	if probe.limit != 100 || probe.timeout != 30*time.Second {
		t.Fatalf("defaults: %+v", probe)
	}
	for _, tc := range []struct {
		seconds float64
		want    time.Duration
	}{{0, 0}, {0.01, 10 * time.Millisecond}, {1000, comitia.MaxMessageWait}} {
		raw, _ := json.Marshal(map[string]any{"context_id": "context", "timeout_seconds": tc.seconds})
		if _, err := s.call(context.Background(), "wait_messages", raw); err != nil || probe.timeout != tc.want {
			t.Fatalf("timeout %v: %v %v", tc.seconds, probe.timeout, err)
		}
	}
	for _, raw := range []string{`{"context_id":"context","timeout_seconds":-1}`, `{"timeout_seconds":0}`, `{"context_id":"context","surprise":true}`} {
		if _, err := s.call(context.Background(), "wait_messages", json.RawMessage(raw)); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("invalid wait %s: %v", raw, err)
		}
	}
	for _, definition := range messageTools() {
		name := definition["name"].(string)
		if name != "replay_messages" && name != "wait_messages" {
			continue
		}
		properties := definition["inputSchema"].(map[string]any)["properties"].(map[string]any)
		for _, field := range []string{"for_instance", "from_instance", "task_id", "schema"} {
			if properties[field] == nil {
				t.Fatalf("%s schema omits %s", name, field)
			}
		}
	}
}

func TestMCPMessageReadRejectsOtherToolFields(t *testing.T) {
	probe := new(messageReadProbe)
	s := &Server{API: probe, Credential: "configured"}
	for _, tc := range []struct{ tool, field string }{{"replay_messages", "timeout_seconds"}, {"wait_messages", "limit"}} {
		for _, value := range []string{"0", "null", "100"} {
			raw := json.RawMessage(`{"context_id":"context","` + tc.field + `":` + value + `}`)
			if _, err := s.call(context.Background(), tc.tool, raw); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("%s accepted %s=%s: %v", tc.tool, tc.field, value, err)
			}
		}
	}
	if probe.token != "" {
		t.Fatal("invalid args reached API")
	}
}
