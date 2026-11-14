package comitia

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"slices"
	"strings"
)

func strictPayload(raw json.RawMessage, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return InvalidJSONFields(err, "payload", raw, target)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return Invalidf("payload must contain one JSON value")
	}
	return nil
}

// Append validation inspects every member before encoding/json can merge duplicate
// objects or accept case-insensitive field aliases. Historical readers use strictPayload.
func strictAppendPayload(raw json.RawMessage, target any) error {
	if err := exactPayloadFields(raw, reflect.TypeOf(target)); err != nil {
		return err
	}
	return strictPayload(raw, target)
}

func appendPayloadValidator(target any, validator PayloadValidator) PayloadValidator {
	return func(raw json.RawMessage) error {
		if err := exactPayloadFields(raw, reflect.TypeOf(target)); err != nil {
			return err
		}
		return validator(raw)
	}
}

func exactPayloadFields(raw json.RawMessage, target reflect.Type) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := scanPayloadValue(d, target); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return Invalidf("payload must contain one JSON value")
	}
	return nil
}

func scanPayloadValue(d *json.Decoder, target reflect.Type) error {
	for target != nil && target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	token, err := d.Token()
	if err != nil {
		return InvalidJSON(err, "payload")
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
				return InvalidJSON(err, "payload")
			}
			key, ok := token.(string)
			if !ok {
				return Invalidf("payload object key must be a string")
			}
			if seen[key] {
				return Invalidf("payload: duplicate key %q", key)
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
					detail := strings.TrimPrefix(UnknownField(key, keys).Error(), ErrInvalid.Error()+": ")
					return Invalidf("payload: %s; keys are case-sensitive", detail)
				}
			} else if target != nil && target.Kind() == reflect.Map {
				child = target.Elem()
			}
			if err := scanPayloadValue(d, child); err != nil {
				return err
			}
		}
		if _, err := d.Token(); err != nil {
			return InvalidJSON(err, "payload")
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
			return InvalidJSON(err, "payload")
		}
	}
	return nil
}

func nonempty(values []string) bool {
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

func validDiscussion(raw json.RawMessage) error { return strictAppendPayload(raw, &struct{}{}) }

func validAssignment(raw json.RawMessage) error {
	var p struct {
		Instructions       string   `json:"instructions"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
	}
	if err := strictAppendPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.Instructions) == "" || !nonempty(p.AcceptanceCriteria) {
		return Invalidf("instructions and acceptance_criteria required")
	}
	return nil
}

func validReviewRequest(raw json.RawMessage) error {
	var p struct {
		ArtifactRefs []string `json:"artifact_refs"`
		Checks       []string `json:"checks"`
	}
	if err := strictAppendPayload(raw, &p); err != nil {
		return err
	}
	if !nonempty(p.ArtifactRefs) || !nonempty(p.Checks) {
		return Invalidf("artifact_refs and checks required")
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
	if err := strictAppendPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.Summary) == "" {
		return Invalidf("summary required")
	}
	if p.Severity != "info" && p.Severity != "warning" && p.Severity != "error" {
		return Invalidf("severity must be info, warning or error")
	}
	if p.Priority != nil && !slices.Contains(findingPriorities, *p.Priority) {
		return Invalidf("priority must be P0, P1, P2 or P3 when present")
	}
	if p.Size != nil && !slices.Contains(findingSizes, *p.Size) {
		return Invalidf("size must be S, M or L when present")
	}
	return nil
}

func validStance(raw json.RawMessage) error {
	var p struct {
		Stance string  `json:"stance"`
		Note   *string `json:"note,omitempty"`
	}
	if err := strictAppendPayload(raw, &p); err != nil {
		return err
	}
	if !slices.Contains(stances, p.Stance) {
		return Invalidf("stance must be agree, refine or disagree")
	}
	if p.Note != nil && strings.TrimSpace(*p.Note) == "" {
		return Invalidf("note must be nonblank when present")
	}
	return nil
}

func validBlocker(raw json.RawMessage) error {
	var p struct {
		Reason string   `json:"reason"`
		Needs  []string `json:"needs"`
	}
	if err := strictAppendPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.Reason) == "" || !nonempty(p.Needs) {
		return Invalidf("reason and needs required")
	}
	return nil
}

func validWorkSubmitted(raw json.RawMessage) error {
	var p struct {
		ArtifactRefs []string `json:"artifact_refs"`
		Summary      string   `json:"summary"`
	}
	if err := strictAppendPayload(raw, &p); err != nil {
		return err
	}
	if !nonempty(p.ArtifactRefs) || strings.TrimSpace(p.Summary) == "" {
		return Invalidf("artifact_refs and summary required")
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
	if err := strictAppendPayload(raw, &p); err != nil {
		return err
	}
	if !nonempty(p.Checks) || p.ArtifactRefs != nil && !nonempty(p.ArtifactRefs) {
		return Invalidf("checks required and artifact_refs must contain nonempty entries")
	}
	switch p.Status {
	case "passed", "failed":
		if !nonempty(p.EvidenceRefs) {
			return Invalidf("evidence_refs required for passed or failed status")
		}
	case "not_run":
		if strings.TrimSpace(p.Reason) == "" {
			return Invalidf("reason required for not_run status")
		}
	default:
		return Invalidf("status must be passed, failed or not_run")
	}
	return nil
}
