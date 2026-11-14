package comitia

import (
	"github.com/krimvp/comitia/internal/schema"
)

// Naming a child requires read on the current context, so a write-only sender
// cannot probe child membership that ListChildContexts would deny it.
func (s *Service) requireTaskStatusRead(r ReadStore, raw, cid string, env *MessageEnvelope) error {
	child, err := schema.TaskStatusChild(env)
	if err != nil || child == "" {
		return err
	}
	_, _, err = s.require(r, raw, cid, PermissionRead)
	return err
}
