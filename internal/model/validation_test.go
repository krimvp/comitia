package model

import (
	"encoding/json"
	"strings"
	"testing"
)

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
