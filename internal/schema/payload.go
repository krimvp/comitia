package schema

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strings"

	"github.com/krimvp/comitia/internal/model"
)

func StrictPayload(raw json.RawMessage, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return model.InvalidJSONFields(err, "payload", raw, target)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return model.Invalidf("payload must contain one JSON value")
	}
	return nil
}

// Append validation inspects every member before encoding/json can merge duplicate
// objects or accept case-insensitive field aliases. Historical readers use strictPayload.
func StrictAppendPayload(raw json.RawMessage, target any) error {
	if err := ExactPayloadFields(raw, reflect.TypeOf(target)); err != nil {
		return err
	}
	return StrictPayload(raw, target)
}

func appendPayloadValidator(target any, validator model.PayloadValidator) model.PayloadValidator {
	return func(raw json.RawMessage) error {
		if err := ExactPayloadFields(raw, reflect.TypeOf(target)); err != nil {
			return err
		}
		return validator(raw)
	}
}

func ExactPayloadFields(raw json.RawMessage, target reflect.Type) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := scanPayloadValue(d, target); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return model.Invalidf("payload must contain one JSON value")
	}
	return nil
}

func scanPayloadValue(d *json.Decoder, target reflect.Type) error {
	for target != nil && target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	token, err := d.Token()
	if err != nil {
		return model.InvalidJSON(err, "payload")
	}
	switch token {
	case json.Delim('{'):
		var fields map[string]reflect.Type
		if target != nil && target.Kind() == reflect.Struct {
			fields = make(map[string]reflect.Type, target.NumField())
			for i := 0; i < target.NumField(); i++ {
				field := target.Field(i)
				if field.PkgPath != "" {
					continue
				}
				name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
				if name == "-" {
					continue
				}
				if name == "" {
					name = field.Name
				}
				fields[name] = field.Type
			}
		}
		seen := make(map[string]bool)
		for d.More() {
			token, err := d.Token()
			if err != nil {
				return model.InvalidJSON(err, "payload")
			}
			key, ok := token.(string)
			if !ok {
				return model.Invalidf("payload object key must be a string")
			}
			if seen[key] {
				return model.Invalidf("payload: duplicate key %q", key)
			}
			seen[key] = true
			var child reflect.Type
			if fields != nil {
				child, ok = fields[key]
				if !ok {
					keys := make([]string, 0, len(fields))
					for name := range fields {
						keys = append(keys, name)
					}
					detail := strings.TrimPrefix(model.UnknownField(key, keys).Error(), model.ErrInvalid.Error()+": ")
					return model.Invalidf("payload: %s; keys are case-sensitive", detail)
				}
			} else if target != nil && target.Kind() == reflect.Map {
				child = target.Elem()
			}
			if err := scanPayloadValue(d, child); err != nil {
				return err
			}
		}
		if _, err := d.Token(); err != nil {
			return model.InvalidJSON(err, "payload")
		}
	case json.Delim('['):
		var child reflect.Type
		if target != nil && (target.Kind() == reflect.Slice || target.Kind() == reflect.Array) {
			child = target.Elem()
		}
		for d.More() {
			if err := scanPayloadValue(d, child); err != nil {
				return err
			}
		}
		if _, err := d.Token(); err != nil {
			return model.InvalidJSON(err, "payload")
		}
	}
	return nil
}

func NonEmpty(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, v := range values {
		if strings.TrimSpace(v) == "" {
			return false
		}
	}
	return true
}

var (
	findingPriorities = []string{"P0", "P1", "P2", "P3"}
	findingSizes      = []string{"S", "M", "L"}
	stances           = []string{"agree", "refine", "disagree"}
)

func validDiscussion(raw json.RawMessage) error { return StrictAppendPayload(raw, &struct{}{}) }

func validAssignment(raw json.RawMessage) error {
	var p struct {
		Instructions       string   `json:"instructions"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
	}
	if err := StrictAppendPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.Instructions) == "" || !NonEmpty(p.AcceptanceCriteria) {
		return model.Invalidf("instructions and acceptance_criteria required")
	}
	return nil
}

func validReviewRequest(raw json.RawMessage) error {
	var p struct {
		ArtifactRefs []string `json:"artifact_refs"`
		Checks       []string `json:"checks"`
	}
	if err := StrictAppendPayload(raw, &p); err != nil {
		return err
	}
	if !NonEmpty(p.ArtifactRefs) || !NonEmpty(p.Checks) {
		return model.Invalidf("artifact_refs and checks required")
	}
	return nil
}

func validFinding(raw json.RawMessage) error {
	var p struct {
		Severity     string   `json:"severity"`
		Summary      string   `json:"summary"`
		EvidenceRefs []string `json:"evidence_refs,omitempty"`
		Priority     *string  `json:"priority,omitempty"`
		Size         *string  `json:"size,omitempty"`
	}
	if err := StrictAppendPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.Summary) == "" {
		return model.Invalidf("summary required")
	}
	if p.Severity != "info" && p.Severity != "warning" && p.Severity != "error" {
		return model.Invalidf("severity must be info, warning or error")
	}
	if p.Priority != nil && !slices.Contains(findingPriorities, *p.Priority) {
		return model.Invalidf("priority must be P0, P1, P2 or P3 when present")
	}
	if p.Size != nil && !slices.Contains(findingSizes, *p.Size) {
		return model.Invalidf("size must be S, M or L when present")
	}
	return nil
}

func validStance(raw json.RawMessage) error {
	var p struct {
		Stance string  `json:"stance"`
		Note   *string `json:"note,omitempty"`
	}
	if err := StrictAppendPayload(raw, &p); err != nil {
		return err
	}
	if !slices.Contains(stances, p.Stance) {
		return model.Invalidf("stance must be agree, refine or disagree")
	}
	if p.Note != nil && strings.TrimSpace(*p.Note) == "" {
		return model.Invalidf("note must be nonblank when present")
	}
	return nil
}

func validBlocker(raw json.RawMessage) error {
	var p struct {
		Reason string   `json:"reason"`
		Needs  []string `json:"needs"`
	}
	if err := StrictAppendPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.Reason) == "" || !NonEmpty(p.Needs) {
		return model.Invalidf("reason and needs required")
	}
	return nil
}

func validWorkSubmitted(raw json.RawMessage) error {
	var p struct {
		ArtifactRefs []string `json:"artifact_refs"`
		Summary      string   `json:"summary"`
	}
	if err := StrictAppendPayload(raw, &p); err != nil {
		return err
	}
	if !NonEmpty(p.ArtifactRefs) || strings.TrimSpace(p.Summary) == "" {
		return model.Invalidf("artifact_refs and summary required")
	}
	return nil
}

func validVerificationResult(raw json.RawMessage) error {
	var p struct {
		Status       string   `json:"status"`
		Checks       []string `json:"checks"`
		EvidenceRefs []string `json:"evidence_refs,omitempty"`
		ArtifactRefs []string `json:"artifact_refs,omitempty"`
		Reason       string   `json:"reason,omitempty"`
	}
	if err := StrictAppendPayload(raw, &p); err != nil {
		return err
	}
	if !NonEmpty(p.Checks) || p.ArtifactRefs != nil && !NonEmpty(p.ArtifactRefs) {
		return model.Invalidf("checks required and artifact_refs must contain nonempty entries")
	}
	switch p.Status {
	case "passed", "failed":
		if !NonEmpty(p.EvidenceRefs) {
			return model.Invalidf("evidence_refs required for passed or failed status")
		}
	case "not_run":
		if strings.TrimSpace(p.Reason) == "" {
			return model.Invalidf("reason required for not_run status")
		}
	default:
		return model.Invalidf("status must be passed, failed or not_run")
	}
	return nil
}
