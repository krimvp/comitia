// Permission names accepted by grant, delegate and credential-check.

package main

import (
	"strings"

	"github.com/krimvp/comitia"
)

// parsePermissions reads a comma-separated --permissions value. On top of the
// canonical names (comitia.ParsePermissions) the CLI trims spaces around each
// name and accepts every canonical name, admin and all included, and repeats;
// an empty name is invalid.
func parsePermissions(s string) (comitia.Permission, error) {
	names := strings.Split(s, ",")
	for i, name := range names {
		names[i] = strings.TrimSpace(name)
	}
	return comitia.ParsePermissions(names...)
}
