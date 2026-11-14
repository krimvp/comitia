// Permission names accepted by grant, delegate and credential-check.

package main

import (
	"strings"

	"github.com/krimvp/comitia"
)

func parsePermissions(s string) (comitia.Permission, error) {
	var p comitia.Permission
	for _, v := range strings.Split(s, ",") {
		switch strings.TrimSpace(v) {
		case "read":
			p |= comitia.PermissionRead
		case "write":
			p |= comitia.PermissionWrite
		case "claim":
			p |= comitia.PermissionClaim
		case "grant":
			p |= comitia.PermissionGrant
		case "admin":
			p |= comitia.PermissionAdmin
		case "create":
			p |= comitia.PermissionCreate
		case "all":
			p |= comitia.PermissionAll
		default:
			return 0, comitia.Invalidf("permissions must be read, write, claim, grant, admin, create or all")
		}
	}
	return p, nil
}
