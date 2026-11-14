package model

// permissionNames is the canonical name of each permission bit, in bit order.
var permissionNames = []struct {
	name string
	bit  Permission
}{
	{"read", PermissionRead},
	{"write", PermissionWrite},
	{"claim", PermissionClaim},
	{"grant", PermissionGrant},
	{"admin", PermissionAdmin},
	{"create", PermissionCreate},
}

// ParsePermissions maps canonical permission names to their bits: read,
// write, claim, grant, admin and create, plus all for PermissionAll. Names
// are exact; repeats are allowed. An empty list is no permissions. Adapters
// layer their own policy (which names they accept, repeats, emptiness, error
// text) on top.
func ParsePermissions(names ...string) (Permission, error) {
	var p Permission
next:
	for _, name := range names {
		if name == "all" {
			p |= PermissionAll
			continue
		}
		for _, entry := range permissionNames {
			if name == entry.name {
				p |= entry.bit
				continue next
			}
		}
		return 0, Invalidf("permissions must be read, write, claim, grant, admin, create or all")
	}
	return p, nil
}

// FormatPermissions returns the canonical names of the bits set in p, in bit
// order; it never returns all. Bits without a name are left out.
func FormatPermissions(p Permission) []string {
	names := []string{}
	for _, entry := range permissionNames {
		if p&entry.bit != 0 {
			names = append(names, entry.name)
		}
	}
	return names
}
