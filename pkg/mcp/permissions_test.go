package mcp

import (
	"reflect"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

// TestParseToolPermissionsPolicy pins which permission names the tools accept
// and the error text of everything else.
func TestParseToolPermissionsPolicy(t *testing.T) {
	const only = "invalid input: permissions must contain only read, write, claim, grant or create"
	for _, tc := range []struct {
		in   []string
		want comitia.Permission
		err  string
	}{
		{[]string{"read"}, comitia.PermissionRead, ""},
		{[]string{"write"}, comitia.PermissionWrite, ""},
		{[]string{"claim"}, comitia.PermissionClaim, ""},
		{[]string{"grant"}, comitia.PermissionGrant, ""},
		{[]string{"create"}, comitia.PermissionCreate, ""},
		{[]string{"read", "write", "claim", "grant", "create"}, comitia.PermissionRead | comitia.PermissionWrite | comitia.PermissionClaim | comitia.PermissionGrant | comitia.PermissionCreate, ""},
		{[]string{"admin"}, 0, only},
		{[]string{"all"}, 0, only},
		{[]string{"read", "admin"}, 0, only},
		{[]string{" read"}, 0, only},
		{[]string{"Read"}, 0, only},
		{[]string{""}, 0, only},
		{[]string{"read,write"}, 0, only},
		{[]string{"read", "read"}, 0, "invalid input: permissions must not repeat a permission"},
		{[]string{}, 0, "invalid input: permissions must not be empty"},
		{nil, 0, "invalid input: permissions must not be empty"},
	} {
		got, err := parseToolPermissions("permissions", tc.in)
		if tc.err == "" {
			if err != nil || got != tc.want {
				t.Errorf("parseToolPermissions(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
			}
		} else if err == nil || err.Error() != tc.err || got != 0 {
			t.Errorf("parseToolPermissions(%q) = %v, %v; want error %q", tc.in, got, err, tc.err)
		}
	}
	if _, err := parseToolPermissions("grants[].permissions", []string{"admin"}); err == nil || err.Error() != "invalid input: grants[].permissions must contain only read, write, claim, grant or create" {
		t.Errorf("field name lost: %v", err)
	}
	if got := permissionSchema()["items"].(map[string]any)["enum"]; !reflect.DeepEqual(got, []string{"read", "write", "claim", "grant", "create"}) {
		t.Errorf("enum = %#v", got)
	}
}
