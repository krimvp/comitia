package schema

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/krimvp/comitia/internal/model"
)

func TestStrictPayloadExactFields(t *testing.T) {
	type item struct {
		Name string `json:"name"`
	}
	type payload struct {
		Item       *item             `json:"item"`
		Items      []item            `json:"items"`
		Dictionary map[string]string `json:"dictionary"`
		Optional   string            `json:"optional,omitempty"`
	}
	for _, raw := range []string{
		`{"item":{"name":"a"},"items":[{"name":"b"}],"dictionary":{"MixedCase":"value"}}`,
		`{"item":null,"items":null,"dictionary":null,"optional":null}`,
	} {
		if err := StrictAppendPayload(json.RawMessage(raw), &payload{}); err != nil {
			t.Fatalf("canonical/null payload: %s: %v", raw, err)
		}
	}
	for _, raw := range []string{
		`{"Item":{"name":"a"}}`, `{"item":{"Name":"a"}}`, `{"items":[{"NAME":"b"}]}`,
		`{"OPTIONAL":null}`, `{"optional":"canonical","Optional":"alias"}`,
		`{"item":{"name":"canonical","Name":"alias"}}`,
	} {
		if err := StrictAppendPayload(json.RawMessage(raw), &payload{}); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("mis-cased payload: %s: %v", raw, err)
		}
	}
}

func TestAppendPayloadScansDictionaryValues(t *testing.T) {
	type item struct {
		Name string `json:"name"`
	}
	type payload struct {
		Values map[string]*item `json:"values"`
	}
	for _, raw := range []string{
		`{"values":{"MixedCase":{"name":"a","name":"b"}}}`,
		`{"values":{"MixedCase":{"Name":"a"}}}`,
		`{"values":{"MixedCase":{"name":"a"},"MixedCase":{"name":"b"}}}`,
	} {
		if err := StrictAppendPayload(json.RawMessage(raw), &payload{}); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("invalid dictionary accepted: %s %v", raw, err)
		}
	}
	if err := StrictAppendPayload(json.RawMessage(`{"values":{"MixedCase":{"name":"a"}}}`), &payload{}); err != nil {
		t.Fatal(err)
	}
	// Scan even object values that will subsequently fail typed decoding.
	if err := ExactPayloadFields(json.RawMessage(`{"values":{"key":[{"id":1,"id":2}]}}`), reflect.TypeOf(&payload{})); !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("untyped nested duplicate: %v", err)
	}
}

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
	err := StrictAppendPayload(raw, &payload)
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
