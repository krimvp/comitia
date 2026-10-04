package mcp

import "github.com/krimvp/comitia"

// instanceID preserves explicit empty IDs while defaulting omitted arguments.
func instanceID(defaults comitia.InstanceDefaults, explicit *string) (string, error) {
	if explicit != nil {
		return defaults.ID(*explicit)
	}
	return defaults.ID()
}

// presenceFilters resolves the instance whose presence a message read records.
func presenceFilters(defaults comitia.InstanceDefaults, f comitia.MessageFilters) (comitia.MessageFilters, error) {
	instance, err := instanceID(defaults, f.AgentInstance)
	f.AgentInstance = nil
	if instance != "" {
		f.AgentInstance = &instance
	}
	return f, err
}
