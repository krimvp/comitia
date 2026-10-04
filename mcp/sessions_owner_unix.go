//go:build unix

package mcp

import (
	"os"
	"syscall"
)

func ownedByCurrentUser(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Geteuid()
}
