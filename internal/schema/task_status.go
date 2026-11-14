package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/krimvp/comitia/internal/model"
)

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

func ValidTaskStatus(raw json.RawMessage) error {
	var p TaskStatusPayload
	if err := StrictPayload(raw, &p); err != nil {
		return err
	}
	if strings.TrimSpace(p.Summary) == "" || len(p.Summary) > 4096 || len(p.ChildContextID) > 128 {
		return model.Invalidf("summary required, at most 4096 bytes; child_context_id at most 128 bytes")
	}
	switch p.State {
	case "planned", "in_progress", "blocked", "review", "done", "failed", "cancelled":
	default:
		return model.Invalidf("state must be planned, in_progress, blocked, review, done, failed or cancelled")
	}
	if !boundedList(p.Blockers) || !boundedList(p.Links) {
		return model.Invalidf("blockers and links must contain at most 64 nonempty entries of at most 1024 bytes")
	}
	return nil
}

func TaskStatusChild(env *model.MessageEnvelope) (string, error) {
	if env == nil || env.Schema != model.TaskStatusSchema {
		return "", nil
	}
	var p TaskStatusPayload
	if json.Unmarshal(env.Payload, &p) != nil {
		return "", model.Invalidf("payload must be a task-status object")
	}
	return p.ChildContextID, nil
}

// The child context must be a direct child of the current context. The sender
// needs no access to the child; readers still need a grant to open it.
func ValidateTaskStatusReferences(r model.ReadStore, cid string, env *model.MessageEnvelope) error {
	childID, err := TaskStatusChild(env)
	if err != nil || childID == "" {
		return err
	}
	child, err := r.Context(childID)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return err
	}
	if err != nil || child.ParentContextID != cid {
		return fmt.Errorf("%w: child_context_id is not a child of this context", model.ErrInvalid)
	}
	if child.TaskID != "" && child.TaskID != env.TaskID {
		return fmt.Errorf("%w: child_context_id serves a different task", model.ErrInvalid)
	}
	return nil
}
