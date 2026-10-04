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
	if strictPayload(raw, &p) != nil || strings.TrimSpace(p.Summary) == "" || len(p.Summary) > 4096 || len(p.ChildContextID) > 128 {
		return ErrInvalid
	}
	switch p.State {
	case "planned", "in_progress", "blocked", "review", "done":
	default:
		return ErrInvalid
	}
	if !boundedList(p.Blockers) || !boundedList(p.Links) {
		return ErrInvalid
	}
	return nil
}

// The child context must be a direct child of the current context. The sender
// needs no access to the child; readers still need a grant to open it.
func validateTaskStatusReferences(r ReadStore, cid string, env *MessageEnvelope) error {
	if env.Schema != TaskStatusSchema {
		return nil
	}
	var p TaskStatusPayload
	if json.Unmarshal(env.Payload, &p) != nil {
		return ErrInvalid
	}
	if p.ChildContextID == "" {
		return nil
	}
	child, err := r.Context(p.ChildContextID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	if err != nil || child.ParentContextID != cid {
		return fmt.Errorf("%w: child_context_id is not a child of this context", ErrInvalid)
	}
	return nil
}
