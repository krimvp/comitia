package comitia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type Message struct {
	TaskAttempt                               *TaskAttempt `json:"task_attempt,omitempty"`
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
	TaskFence       *TaskFence `json:"task_fence,omitempty"`
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
	// ReplyTo selects direct replies; ThreadID includes the root and descendants.
	ReplyTo  string `json:"reply_to,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
	// FromSession selects the authenticated author, independent of instance claims.
	FromSession string `json:"from_session,omitempty"`
	// MaxBytes bounds the encoded messages array; zero uses DefaultMessageBytes.
	MaxBytes     int    `json:"max_bytes,omitempty"`
	ForInstance  string `json:"for_instance,omitempty"`
	FromInstance string `json:"from_instance,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
	Schema       string `json:"schema,omitempty"`
	// StrictRecipients requires both nonempty recipient lists to match.
	// It is opt-in and requires ForInstance.
	StrictRecipients bool `json:"strict_recipients,omitempty"`
	// AgentInstance names the caller's own instance for presence; it never
	// narrows results. Adapters apply configured defaults when it is nil.
	AgentInstance *string `json:"agent_instance,omitempty"`
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
	Messages(string, string, int64, int, ...MessageFilters) ([]Message, error)
}
type MessageWriteStore interface {
	PutMessage(Message) (Message, error)
	Ack(string, string, int64, time.Time, string) error
}

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

func builtinMessageSchemas() map[string]PayloadValidator {
	return map[string]PayloadValidator{
		"comitia.discussion.v1": func(raw json.RawMessage) error { return strictAppendPayload(raw, &struct{}{}) },
		"comitia.assignment.v1": func(raw json.RawMessage) error {
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
		},
		"comitia.review-request.v1": func(raw json.RawMessage) error {
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
		},
		"comitia.finding.v1": func(raw json.RawMessage) error {
			var p struct {
				Severity     string   `json:"severity"`
				Summary      string   `json:"summary"`
				EvidenceRefs []string `json:"evidence_refs,omitempty"`
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
			return nil
		},
		"comitia.blocker.v1": func(raw json.RawMessage) error {
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
		},
		"comitia.work-submitted.v1": func(raw json.RawMessage) error {
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
		},
		"comitia.verification-result.v1": func(raw json.RawMessage) error {
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
		},
		EvaluationResultSchema: appendPayloadValidator(&EvaluationResultPayload{}, validEvaluationResult),
		ExecutionEventSchema:   appendPayloadValidator(&executionEventPayload{}, validExecutionEvent),
		TaskStatusSchema:       appendPayloadValidator(&TaskStatusPayload{}, validTaskStatus),
		TaskEventSchema:        appendPayloadValidator(&TaskEventPayload{}, validTaskEvent),
		RequestSchema:          validRequest,
		ReceiptSchema:          validReceipt,
		MergeCertificateSchema: validMergeCertificate,
		MergeReceiptSchema:     validMergeReceipt,
		ApprovalSchema:         appendPayloadValidator(&ApprovalPayload{}, validApproval),
	}
}

// Normalization owns its slices and payload and makes retries independent of
// recipient ordering, insignificant JSON whitespace and object-key ordering.
func (s *Service) normalizeEnvelope(in *MessageEnvelope, body string) (*MessageEnvelope, error) {
	if in == nil {
		return nil, nil
	}
	if in.Schema == TaskEventSchema {
		return nil, Invalidf("task events are server-authored")
	}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > 65536 || len(in.RecipientSessionIDs) > 64 || len(in.RecipientInstances) > 64 {
		return nil, Invalidf("envelope must be valid JSON, at most 65536 bytes with at most 64 recipient_session_ids and recipient_instances")
	}
	var out MessageEnvelope
	if json.Unmarshal(raw, &out) != nil {
		return nil, Invalidf("envelope must be valid JSON")
	}
	validator, ok := s.messageSchemas[out.Schema]
	if !ok {
		return nil, s.invalidMessageSchema()
	}
	var payload map[string]any
	d := json.NewDecoder(bytes.NewReader(out.Payload))
	d.UseNumber()
	if d.Decode(&payload) != nil || payload == nil {
		return nil, Invalidf("payload must be a JSON object")
	}
	if d.Decode(new(any)) != io.EOF {
		return nil, Invalidf("payload must contain one JSON value")
	}
	if err := validator(out.Payload); err != nil {
		if strings.HasPrefix(out.Schema, "comitia.") {
			return nil, err
		}
		return nil, Invalidf("payload does not match extension schema")
	}
	out.Payload, err = json.Marshal(payload)
	if err != nil {
		return nil, Invalidf("payload must be valid JSON")
	}
	descriptor := s.messageSchemaDescriptors[out.Schema]
	if descriptor.BodyRequired && strings.TrimSpace(body) == "" {
		return nil, Invalidf("body required for %s", out.Schema)
	}
	if descriptor.TaskRequired && out.TaskID == "" {
		return nil, fmt.Errorf("%w: task_id required", ErrInvalid)
	}
	if descriptor.TaskOrArtifactRefs && out.TaskID == "" {
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
	sort.Strings(out.RecipientSessionIDs)
	for i, id := range out.RecipientSessionIDs {
		if id == "" || (i > 0 && id == out.RecipientSessionIDs[i-1]) {
			return nil, Invalidf("recipient_session_ids must be nonempty and unique")
		}
	}
	sort.Strings(out.RecipientInstances)
	for i, instance := range out.RecipientInstances {
		if !validInstanceID(instance) || (i > 0 && instance == out.RecipientInstances[i-1]) {
			return nil, Invalidf("recipient_instances must be valid instance IDs and unique")
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
			return referenceError{"task_id"}
		}
	}
	if env.ReplyTo != "" {
		message, err := r.Message(env.ReplyTo)
		if err != nil || message.ContextID != cid {
			return referenceError{"reply_to"}
		}
	}
	if err := validateTaskStatusReferences(r, cid, env); err != nil {
		return err
	}
	now := s.now().UTC()
	for i, sid := range env.RecipientSessionIDs {
		failure := referenceError{fmt.Sprintf("recipient_session_ids[%d]", i)}
		session, err := r.Session(sid)
		if err != nil || session.RevokedAt != nil || !session.ExpiresAt.After(now) {
			return failure
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
			return failure
		}
	}
	return nil
}

// Names are display labels, never routing keys. IDs identify concrete sessions.
func (s *Service) ListParticipants(ctx context.Context, token, cid string, limit int) ([]Participant, error) {
	if limit < 1 || limit > 1000 {
		return nil, Invalidf("limit must be between 1 and 1000")
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
	if !validInstanceID(in.ID) {
		return nil, Invalidf("agent_instance.id must be 1-64 ASCII letters, digits, dot, underscore or hyphen")
	}
	if len(in.Label) > 128 || !utf8.ValidString(in.Label) {
		return nil, Invalidf("agent_instance.label must be valid UTF-8 and at most 128 bytes")
	}
	for _, c := range in.Label {
		if unicode.IsControl(c) {
			return nil, Invalidf("agent_instance.label must not contain control characters")
		}
	}
	out := *in
	return &out, nil
}

// Omitting the instance (or supplying an empty ID) selects the legacy cursor.
func cursorInstance(instances []string) (string, error) {
	if len(instances) > 1 {
		return "", Invalidf("agent_instance accepts at most one instance")
	}
	if len(instances) == 0 || instances[0] == "" {
		return "", nil
	}
	if !validInstanceID(instances[0]) {
		return "", Invalidf("agent_instance must be 1-64 ASCII letters, digits, dot, underscore or hyphen")
	}
	return instances[0], nil
}

func sameAgentInstance(a, b *AgentInstance) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (s *Service) invalidMessageSchema() error {
	ids := make([]string, 0, len(s.messageSchemas))
	for id := range s.messageSchemas {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return Invalidf("schema must name a registered message schema; use list_message_schemas or get_message_schema; registered schemas: %s", strings.Join(ids, ", "))
}

func (s *Service) validateMessageReferenceAccess(r ReadStore, a authInfo, cid string, env *MessageEnvelope) error {
	if env == nil {
		return nil
	}
	field := ""
	switch {
	case env.ReplyTo != "":
		field = "reply_to"
	case env.TaskID != "":
		field = "task_id"
	case len(env.RecipientSessionIDs) > 0:
		field = "recipient_session_ids[0]"
	}
	if field == "" {
		return nil
	}
	// Check effective (including inherited) read before looking up any target or
	// returning a deduplicated reply, so write-only callers cannot probe IDs.
	if _, _, err := s.requireAuthenticated(r, a, cid, PermissionRead); err != nil {
		if errors.Is(err, ErrForbidden) {
			return referenceError{field}
		}
		return err
	}
	return nil
}
