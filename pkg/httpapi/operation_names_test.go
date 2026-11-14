package httpapi

import (
	"sort"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

// TestEveryRouteHasOperationName keeps request logs, diagnostics and
// operation-specific hints from labelling a route "unknown".
func TestEveryRouteHasOperationName(t *testing.T) {
	routes := (&handler{}).endpoints()
	var names []string
	for route := range routes {
		names = append(names, route)
	}
	sort.Strings(names)
	for _, route := range names {
		if operation := comitia.SafeOperation(route); operation == "unknown" {
			t.Errorf("route %q has no operation name", route)
		}
	}
}
