package sqlstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/krimvp/comitia/internal/model"
)

func TestSQLitePrivateFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private.db")
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		info, err := os.Stat(path + suffix)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("%s mode = %o", suffix, info.Mode().Perm())
		}
	}
}

func TestSQLiteRejectsUnsafeFiles(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		t.Run(suffix, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "unsafe.db")
			if err := os.WriteFile(path, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path+suffix, nil, 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path+suffix, 0644); err != nil {
				t.Fatal(err)
			}
			st, err := OpenSQLite(path)
			if st != nil {
				st.Close()
			}
			if !errors.Is(err, model.ErrInvalid) {
				t.Fatalf("unsafe %s: %v", suffix, err)
			}
			info, _ := os.Stat(path + suffix)
			if info != nil && info.Mode().Perm() != 0644 {
				t.Fatal("modified unsafe existing file")
			}
		})
	}
}

func TestSQLiteRejectsSymlinkFiles(t *testing.T) {
	for _, suffix := range []string{"", "-wal", "-shm"} {
		t.Run(suffix, func(t *testing.T) {
			dir := t.TempDir()
			path, target := filepath.Join(dir, "state.db"), filepath.Join(dir, "target")
			if err := os.WriteFile(target, nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path+suffix); err != nil {
				t.Fatal(err)
			}
			st, err := OpenSQLite(path)
			if st != nil {
				st.Close()
			}
			if !errors.Is(err, model.ErrInvalid) {
				t.Fatalf("symlink %s: %v", suffix, err)
			}
		})
	}
}
