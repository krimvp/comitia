package comitia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/krimvp/comitia/internal/model"
)

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
	if out.Schema == StanceSchema && out.ReplyTo == "" {
		return nil, Invalidf("reply_to required for %s", out.Schema)
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
		if !model.ValidInstanceID(instance) || (i > 0 && instance == out.RecipientInstances[i-1]) {
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
			return model.ReferenceError{Field: "task_id"}
		}
	}
	if env.ReplyTo != "" {
		message, err := r.Message(env.ReplyTo)
		if err != nil || message.ContextID != cid {
			return model.ReferenceError{Field: "reply_to"}
		}
	}
	if err := validateTaskStatusReferences(r, cid, env); err != nil {
		return err
	}
	return s.validateReaders(r, cid, "recipient_session_ids", env.RecipientSessionIDs)
}

// validateReaders checks that every listed session can read the context,
// naming the offending field[i] in the error.
func (s *Service) validateReaders(r ReadStore, cid, field string, sids []string) error {
	now := s.now().UTC()
	for i, sid := range sids {
		failure := model.ReferenceError{Field: fmt.Sprintf("%s[%d]", field, i)}
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
			return model.ReferenceError{Field: field}
		}
		return err
	}
	return nil
}
