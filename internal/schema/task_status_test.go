package schema

import (
	"encoding/json"
	"strings"
	"testing"
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
		if err := ValidTaskStatus(json.RawMessage(raw)); (err == nil) != ok {
			t.Errorf("%.80s: %v", raw, err)
		}
	}
	many := make([]string, 65)
	for i := range many {
		many[i] = "b"
	}
	raw, _ := json.Marshal(TaskStatusPayload{State: "blocked", Summary: "s", Blockers: many})
	if ValidTaskStatus(raw) == nil {
		t.Fatal("unbounded blockers accepted")
	}
}
