//go:build unix

package mcp

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

type foreignOwner struct{ os.FileInfo }

func (f foreignOwner) Sys() any {
	st := *f.FileInfo.Sys().(*syscall.Stat_t)
	st.Uid++
	return &st
}

func TestSessionsDirectoryOwnership(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	root, err := privateSessionsDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		t.Fatal(err)
	}
	if !ownedByCurrentUser(info) || ownedByCurrentUser(foreignOwner{info}) {
		t.Fatal("ownership check")
	}
}
