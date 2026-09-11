package comitia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

func strictPayload(raw json.RawMessage, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return ErrInvalid
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return ErrInvalid
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

func builtinMessageSchemas() map[string]PayloadValidator {
	return map[string]PayloadValidator{
		"comitia.discussion.v1": func(raw json.RawMessage) error { return strictPayload(raw, &struct{}{}) },
		"comitia.assignment.v1": func(raw json.RawMessage) error {
			var p struct {
				Instructions       string   `json:"instructions"`
				AcceptanceCriteria []string `json:"acceptance_criteria"`
			}
			if strictPayload(raw, &p) != nil || strings.TrimSpace(p.Instructions) == "" || !nonempty(p.AcceptanceCriteria) {
				return ErrInvalid
			}
			return nil
		},
		"comitia.review-request.v1": func(raw json.RawMessage) error {
			var p struct {
				ArtifactRefs []string `json:"artifact_refs"`
				Checks       []string `json:"checks"`
			}
			if strictPayload(raw, &p) != nil || !nonempty(p.ArtifactRefs) || !nonempty(p.Checks) {
				return ErrInvalid
			}
			return nil
		},
		"comitia.finding.v1": func(raw json.RawMessage) error {
			var p struct {
				Severity     string   `json:"severity"`
				Summary      string   `json:"summary"`
				EvidenceRefs []string `json:"evidence_refs,omitempty"`
			}
			if strictPayload(raw, &p) != nil || strings.TrimSpace(p.Summary) == "" {
				return ErrInvalid
			}
			if p.Severity != "info" && p.Severity != "warning" && p.Severity != "error" {
				return ErrInvalid
			}
			return nil
		},
		"comitia.blocker.v1": func(raw json.RawMessage) error {
			var p struct {
				Reason string   `json:"reason"`
				Needs  []string `json:"needs"`
			}
			if strictPayload(raw, &p) != nil || strings.TrimSpace(p.Reason) == "" || !nonempty(p.Needs) {
				return ErrInvalid
			}
			return nil
		},
		"comitia.work-submitted.v1": func(raw json.RawMessage) error {
			var p struct {
				ArtifactRefs []string `json:"artifact_refs"`
				Summary      string   `json:"summary"`
			}
			if strictPayload(raw, &p) != nil || !nonempty(p.ArtifactRefs) || strings.TrimSpace(p.Summary) == "" {
				return ErrInvalid
			}
			return nil
		},
		"comitia.verification-result.v1": func(raw json.RawMessage) error {
			var p struct {
				Status       string   `json:"status"`
				Checks       []string `json:"checks"`
				EvidenceRefs []string `json:"evidence_refs,omitempty"`
				Reason       string   `json:"reason,omitempty"`
			}
			if strictPayload(raw, &p) != nil || !nonempty(p.Checks) {
				return ErrInvalid
			}
			switch p.Status {
			case "passed", "failed":
				if !nonempty(p.EvidenceRefs) {
					return ErrInvalid
				}
			case "not_run":
				if strings.TrimSpace(p.Reason) == "" {
					return ErrInvalid
				}
			default:
				return ErrInvalid
			}
			return nil
		},
		EvaluationResultSchema: validEvaluationResult,
		ExecutionEventSchema:   validExecutionEvent,
	}
}

// Normalization owns its slices and payload and makes retries independent of
// recipient ordering, insignificant JSON whitespace and object-key ordering.
func (s *Service) normalizeEnvelope(in *MessageEnvelope, body string) (*MessageEnvelope, error) {
	if in == nil {
		return nil, nil
	}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > 65536 || len(in.RecipientSessionIDs) > 64 {
		return nil, ErrInvalid
	}
	var out MessageEnvelope
	if json.Unmarshal(raw, &out) != nil {
		return nil, ErrInvalid
	}
	validator, ok := s.messageSchemas[out.Schema]
	if !ok {
		return nil, fmt.Errorf("%w: unknown message schema", ErrInvalid)
	}
	var payload map[string]any
	d := json.NewDecoder(bytes.NewReader(out.Payload))
	d.UseNumber()
	if d.Decode(&payload) != nil || payload == nil {
		return nil, ErrInvalid
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, ErrInvalid
	}
	if validator(out.Payload) != nil {
		return nil, fmt.Errorf("%w: payload does not match schema", ErrInvalid)
	}
	out.Payload, err = json.Marshal(payload)
	if err != nil {
		return nil, ErrInvalid
	}
	if out.Schema == "comitia.discussion.v1" && strings.TrimSpace(body) == "" {
		return nil, ErrInvalid
	}
	switch out.Schema {
	case "comitia.assignment.v1", "comitia.review-request.v1", "comitia.blocker.v1", "comitia.work-submitted.v1", "comitia.verification-result.v1", EvaluationResultSchema, ExecutionEventSchema:
		if out.TaskID == "" {
			return nil, fmt.Errorf("%w: task_id required", ErrInvalid)
		}
	}
	sort.Strings(out.RecipientSessionIDs)
	for i, id := range out.RecipientSessionIDs {
		if id == "" || (i > 0 && id == out.RecipientSessionIDs[i-1]) {
			return nil, ErrInvalid
		}
	}
	return &out, nil
}

func sameEnvelope(a, b *MessageEnvelope) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func (s *Service) validateEnvelopeReferences(r ReadStore, cid string, env *MessageEnvelope) error {
	if env == nil {
		return nil
	}
	if env.TaskID != "" {
		task, err := r.Task(env.TaskID)
		if err != nil || task.ContextID != cid {
			return ErrForbidden
		}
	}
	if env.ReplyTo != "" {
		message, err := r.Message(env.ReplyTo)
		if err != nil || message.ContextID != cid {
			return ErrForbidden
		}
	}
	now := s.now().UTC()
	for _, sid := range env.RecipientSessionIDs {
		session, err := r.Session(sid)
		if err != nil || session.RevokedAt != nil || !session.ExpiresAt.After(now) {
			return ErrForbidden
		}
		grants, err := r.Grants(sid, cid, now)
		if err != nil {
			return err
		}
		readable := false
		for _, g := range grants {
			if g.Permissions&PermissionRead != 0 {
				readable = true
				break
			}
		}
		if !readable {
			return ErrForbidden
		}
	}
	return nil
}

// Names are display labels, never routing keys. IDs identify concrete sessions.
func (s *Service) ListParticipants(ctx context.Context, token, cid string, limit int) ([]Participant, error) {
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalid
	}
	out := []Participant{}
	err := s.store.View(ctx, func(r ReadStore) error {
		if _, _, err := s.require(r, token, cid, PermissionRead); err != nil {
			return err
		}
		var err error
		out, err = r.Participants(cid, s.now().UTC(), limit)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
