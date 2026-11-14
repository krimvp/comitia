package main

import (
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

// TestParsePermissionsPolicy pins what --permissions accepts and the error
// text of everything else.
func TestParsePermissionsPolicy(t *testing.T) {
	const invalid = "invalid input: permissions must be read, write, claim, grant, admin, create or all"
	for _, tc := range []struct {
		in   string
		want comitia.Permission
		err  string
	}{
		{"read", comitia.PermissionRead, ""},
		{"write", comitia.PermissionWrite, ""},
		{"claim", comitia.PermissionClaim, ""},
		{"grant", comitia.PermissionGrant, ""},
		{"admin", comitia.PermissionAdmin, ""},
		{"create", comitia.PermissionCreate, ""},
		{"all", comitia.PermissionAll, ""},
		{"read,write", comitia.PermissionRead | comitia.PermissionWrite, ""},
		{" read , claim ", comitia.PermissionRead | comitia.PermissionClaim, ""},
		{"read,read", comitia.PermissionRead, ""},
		{"all,read", comitia.PermissionAll, ""},
		{"", 0, invalid},
		{"read,", 0, invalid},
		{"Read", 0, invalid},
		{"owner", 0, invalid},
		{"read;write", 0, invalid},
	} {
		got, err := parsePermissions(tc.in)
		if tc.err == "" {
			if err != nil || got != tc.want {
				t.Errorf("parsePermissions(%q) = %v, %v; want %v", tc.in, got, err, tc.want)
			}
		} else if err == nil || err.Error() != tc.err || got != 0 {
			t.Errorf("parsePermissions(%q) = %v, %v; want error %q", tc.in, got, err, tc.err)
		}
	}
}
