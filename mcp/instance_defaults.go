package mcp

import "github.com/krimvp/comitia"

// instanceID preserves explicit empty IDs while defaulting omitted arguments.
func instanceID(defaults comitia.InstanceDefaults, explicit *string) (string, error) {
	if explicit != nil {
		return defaults.ID(*explicit)
	}
	return defaults.ID()
}
