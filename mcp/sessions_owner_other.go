//go:build !unix

package mcp

import "os"

// ownedByCurrentUser has no portable owner check here; the mode check still applies.
func ownedByCurrentUser(os.FileInfo) bool { return true }
