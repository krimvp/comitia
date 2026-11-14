//go:build !unix

// Sessions directory ownership check where no portable owner check exists.

package mcp

import "os"

// ownedByCurrentUser has no portable owner check here; the mode check still applies.
func ownedByCurrentUser(os.FileInfo) bool { return true }
