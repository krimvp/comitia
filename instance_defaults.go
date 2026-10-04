package comitia

import (
	"fmt"
	"os"
)

// InstanceDefaults holds validated process defaults. Its zero value preserves
// legacy session-scoped behavior. Instance metadata never grants permissions.
type InstanceDefaults struct {
	instance *AgentInstance
}

func NewInstanceDefaults(instance *AgentInstance) (InstanceDefaults, error) {
	instance, err := normalizeAgentInstance(instance)
	return InstanceDefaults{instance: instance}, err
}

// InstanceDefaultsFromEnv captures the environment once at startup. Empty values
// disable defaults; a label without an ID and malformed values are errors.
func InstanceDefaultsFromEnv() (InstanceDefaults, error) {
	id, label := os.Getenv("COMITIA_AGENT_INSTANCE"), os.Getenv("COMITIA_AGENT_INSTANCE_LABEL")
	if id == "" && label == "" {
		return InstanceDefaults{}, nil
	}
	d, err := NewInstanceDefaults(&AgentInstance{ID: id, Label: label})
	if err != nil {
		return InstanceDefaults{}, fmt.Errorf("%w: COMITIA_AGENT_INSTANCE / COMITIA_AGENT_INSTANCE_LABEL", err)
	}
	return d, nil
}

// Message uses the complete explicit object when supplied, otherwise a copy of
// the default. An explicit ID never inherits another instance's display label.
func (d InstanceDefaults) Message(explicit *AgentInstance) *AgentInstance {
	if explicit == nil {
		explicit = d.instance
	}
	if explicit == nil {
		return nil
	}
	out := *explicit
	return &out
}

// ID resolves an optional instance argument. An explicit empty string selects
// the legacy cursor, even when a process default is configured.
func (d InstanceDefaults) ID(explicit ...string) (string, error) {
	if len(explicit) != 0 {
		return cursorInstance(explicit)
	}
	if d.instance != nil {
		return d.instance.ID, nil
	}
	return "", nil
}
