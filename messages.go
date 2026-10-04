package comitia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Message struct {
	ID, ContextID, SessionID, DedupeKey, Body string
	Sequence                                  int64
	CreatedAt                                 time.Time
	Envelope                                  *MessageEnvelope `json:"envelope,omitempty"`
	AgentInstance                             *AgentInstance   `json:"agent_instance,omitempty"`
	AgentInstanceSelfReported                 bool             `json:"agent_instance_self_reported,omitempty"`
}

// AgentInstance is caller-claimed coordination metadata, never an authenticated identity.
type AgentInstance struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}
type MessageInput struct {
	DedupeKey, Body string
	Envelope        *MessageEnvelope `json:"envelope,omitempty"`
	AgentInstance   *AgentInstance   `json:"agent_instance,omitempty"`
}
type MessageEnvelope struct {
	Schema              string          `json:"schema"`
	RecipientSessionIDs []string        `json:"recipient_session_ids,omitempty"`
	RecipientInstances  []string        `json:"recipient_instances,omitempty"`
	TaskID              string          `json:"task_id,omitempty"`
	ReplyTo             string          `json:"reply_to,omitempty"`
	Payload             json.RawMessage `json:"payload"`
}

// MessageFilters narrow replay within the same context read permission.
// Nonempty fields are combined with AND; routing metadata is self-reported.
type MessageFilters struct {
	ForInstance  string `json:"for_instance,omitempty"`
	FromInstance string `json:"from_instance,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
	Schema       string `json:"schema,omitempty"`
}

type PayloadValidator func(json.RawMessage) error
type Participant struct {
	SessionID, PrincipalID, Name string
	ExpiresAt                    time.Time
}

type MessageAPI interface {
	ListParticipants(context.Context, string, string, int) ([]Participant, error)
	AppendMessage(context.Context, string, string, MessageInput) (Message, error)
	ReplayMessages(context.Context, string, string, int64, int, ...MessageFilters) ([]Message, error)
	AckMessages(context.Context, string, string, int64, ...string) error
	MessageCursor(context.Context, string, string, ...string) (int64, error)
	WaitMessages(context.Context, string, string, int64, time.Duration, ...MessageFilters) ([]Message, error)
}
type MessageReadStore interface {
	Message(string) (Message, error)
	Participants(string, time.Time, int) ([]Participant, error)
	MessageByDedupe(string, string, string, ...string) (Message, error)
	AckCursor(string, string, string) (int64, error)
	Messages(string, int64, int, ...MessageFilters) ([]Message, error)
}
type MessageWriteStore interface {
	PutMessage(Message) (Message, error)
	Ack(string, string, int64, time.Time, string) error
}

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
				ArtifactRefs []string `json:"artifact_refs,omitempty"`
				Reason       string   `json:"reason,omitempty"`
			}
			if strictPayload(raw, &p) != nil || !nonempty(p.Checks) || p.ArtifactRefs != nil && !nonempty(p.ArtifactRefs) {
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
		TaskStatusSchema:       validTaskStatus,
		ApprovalSchema:         validApproval,
	}
}

// Normalization owns its slices and payload and makes retries independent of
// recipient ordering, insignificant JSON whitespace and object-key ordering.
func (s *Service) normalizeEnvelope(in *MessageEnvelope, body string) (*MessageEnvelope, error) {
	if in == nil {
		return nil, nil
	}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > 65536 || len(in.RecipientSessionIDs) > 64 || len(in.RecipientInstances) > 64 {
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
	case "comitia.assignment.v1", "comitia.review-request.v1", "comitia.blocker.v1", EvaluationResultSchema, ExecutionEventSchema:
		if out.TaskID == "" {
			return nil, fmt.Errorf("%w: task_id required", ErrInvalid)
		}
	case "comitia.work-submitted.v1", "comitia.verification-result.v1":
		// Without a task, exact artifact refs must identify the work instead.
		if out.TaskID != "" {
			break
		}
		var p struct {
			ArtifactRefs []string `json:"artifact_refs"`
		}
		if json.Unmarshal(out.Payload, &p) != nil || len(p.ArtifactRefs) == 0 {
			return nil, fmt.Errorf("%w: task_id or artifact_refs required", ErrInvalid)
		}
		for _, ref := range p.ArtifactRefs {
			if !exactArtifactRef(ref) {
				return nil, fmt.Errorf("%w: artifact_refs must be exact without task_id", ErrInvalid)
			}
		}
	}
	if out.Schema == TaskStatusSchema && out.TaskID == "" {
		return nil, fmt.Errorf("%w: task_id required", ErrInvalid)
	}
	sort.Strings(out.RecipientSessionIDs)
	for i, id := range out.RecipientSessionIDs {
		if id == "" || (i > 0 && id == out.RecipientSessionIDs[i-1]) {
			return nil, ErrInvalid
		}
	}
	sort.Strings(out.RecipientInstances)
	for i, instance := range out.RecipientInstances {
		if !validInstanceID(instance) || (i > 0 && instance == out.RecipientInstances[i-1]) {
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
	if err := validateTaskStatusReferences(r, cid, env); err != nil {
		return err
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

// Instance IDs are short, printable routing keys. They confer no permissions.
func validInstanceID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func normalizeAgentInstance(in *AgentInstance) (*AgentInstance, error) {
	if in == nil {
		return nil, nil
	}
	if !validInstanceID(in.ID) || len(in.Label) > 128 || !utf8.ValidString(in.Label) {
		return nil, ErrInvalid
	}
	for _, c := range in.Label {
		if unicode.IsControl(c) {
			return nil, ErrInvalid
		}
	}
	out := *in
	return &out, nil
}

// Omitting the instance (or supplying an empty ID) selects the legacy cursor.
func cursorInstance(instances []string) (string, error) {
	if len(instances) > 1 {
		return "", ErrInvalid
	}
	if len(instances) == 0 || instances[0] == "" {
		return "", nil
	}
	if !validInstanceID(instances[0]) {
		return "", ErrInvalid
	}
	return instances[0], nil
}

func sameAgentInstance(a, b *AgentInstance) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
