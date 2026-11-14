package model

import (
	"errors"
	"strings"
	"testing"
)

func TestPermissionNames(t *testing.T) {
	for _, name := range FormatPermissions(PermissionAll) {
		p, err := ParsePermissions(name)
		if err != nil || len(FormatPermissions(p)) != 1 || FormatPermissions(p)[0] != name {
			t.Fatalf("%s: %v %v", name, p, err)
		}
	}
	if got := strings.Join(FormatPermissions(PermissionAll), ","); got != "read,write,claim,grant,admin,create" {
		t.Fatalf("names=%s", got)
	}
	if p, err := ParsePermissions("all"); err != nil || p != PermissionAll {
		t.Fatalf("all: %v %v", p, err)
	}
	if p, err := ParsePermissions(); err != nil || p != 0 {
		t.Fatalf("empty: %v %v", p, err)
	}
	for _, bad := range []string{"", "Read", " read", "owner"} {
		if _, err := ParsePermissions("read", bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}
