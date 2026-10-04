package comitia

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// TaskStatusSchema reports a task's progress in the parent context, typically
// from a coordinator working in a private child context. Nothing is relayed
// between contexts; the sender posts the summary itself.
const TaskStatusSchema = "comitia.task-status.v1"

type TaskStatusPayload struct {
	State          string   `json:"state"`
	Summary        string   `json:"summary"`
	ChildContextID string   `json:"child_context_id,omitempty"`
	Blockers       []string `json:"blockers,omitempty"`
	Links          []string `json:"links,omitempty"`
}

func boundedList(values []string) bool {
	if len(values) > 64 {
		return false
	}
	for _, v := range values {
		if strings.TrimSpace(v) == "" || len(v) > 1024 {
			return false
		}
	}
	return true
}

func validTaskStatus(raw json.RawMessage) error {
	var p TaskStatusPayload
	if err := strictPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.Summary) == "" || len(p.Summary) > 4096 || len(p.ChildContextID) > 128 {
		return Invalidf("summary required, at most 4096 bytes; child_context_id at most 128 bytes")
	}
	switch p.State {
	case "planned", "in_progress", "blocked", "review", "done":
	default:
		return Invalidf("state must be planned, in_progress, blocked, review or done")
	}
	if !boundedList(p.Blockers) || !boundedList(p.Links) {
		return Invalidf("blockers and links must contain at most 64 nonempty entries of at most 1024 bytes")
	}
	return nil
}

func taskStatusChild(env *MessageEnvelope) (string, error) {
	if env == nil || env.Schema != TaskStatusSchema {
		return "", nil
	}
	var p TaskStatusPayload
	if json.Unmarshal(env.Payload, &p) != nil {
		return "", Invalidf("payload must be a task-status object")
	}
	return p.ChildContextID, nil
}

// Naming a child requires read on the current context, so a write-only sender
// cannot probe child membership that ListChildContexts would deny it.
func (s *Service) requireTaskStatusRead(r ReadStore, raw, cid string, env *MessageEnvelope) error {
	child, err := taskStatusChild(env)
	if err != nil || child == "" {
		return err
	}
	_, _, err = s.require(r, raw, cid, PermissionRead)
	return err
}

// The child context must be a direct child of the current context. The sender
// needs no access to the child; readers still need a grant to open it.
func validateTaskStatusReferences(r ReadStore, cid string, env *MessageEnvelope) error {
	childID, err := taskStatusChild(env)
	if err != nil || childID == "" {
		return err
	}
	child, err := r.Context(childID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err != nil || child.ParentContextID != cid {
		return fmt.Errorf("%w: child_context_id is not a child of this context", ErrInvalid)
	}
	return nil
}
