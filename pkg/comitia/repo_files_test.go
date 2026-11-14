package comitia_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// repositoryFiles returns the module root and the files the repository
// guards look at, module-relative with forward slashes, sorted.
//
// In a git work tree these are the files git would commit: tracked files
// plus untracked files that no .gitignore excludes (git ls-files --cached
// --others --exclude-standard), less tracked files deleted from the tree.
// CI runs on a clean checkout, where that is exactly the tracked set. On a
// developer's machine ignored leftovers are not seen (dist/, node_modules/,
// or the ignored httpapi/ui-tests/browser/node_modules/ an old clone keeps
// after the move to pkg/), but a file that is new and not yet added is, so a
// new directory fails here before it is committed rather than first in CI.
// Keep a scratch file out of the guards with .git/info/exclude.
//
// Without git (a source archive, or a tree that is not its own repository)
// every file under the root is listed, skipping .git, node_modules and
// __pycache__ directories; such a tree holds no ignored build output to
// speak of.
func repositoryFiles(t *testing.T) (string, []string) {
	t.Helper()
	root := moduleRoot(t)
	files, ok := gitFiles(t, root)
	if !ok {
		files = walkFiles(t, root)
	}
	sort.Strings(files)
	return root, files
}

func gitFiles(t *testing.T, root string) ([]string, bool) {
	t.Helper()
	top, err := gitOutput(root, "rev-parse", "--show-toplevel")
	if err != nil || !sameDir(strings.TrimSpace(string(top)), root) {
		return nil, false
	}
	out, err := gitOutput(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		t.Fatalf("git ls-files: %v", err)
	}
	var files []string
	for _, rel := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		if rel == "" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatal(err)
		}
		files = append(files, rel)
	}
	return files, true
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Output()
}

func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func walkFiles(t *testing.T, root string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// TestRepositoryFilesWithoutGit checks the fallback for a tree without git
// on a copy of a few files: it must list them and skip the directories named
// above.
func TestRepositoryFilesWithoutGit(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"go.mod", "cmd/x/main.go", "node_modules/m/m.go", "a/__pycache__/c.pyc", ".git/HEAD"} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	got := walkFiles(t, root)
	sort.Strings(got)
	if want := "cmd/x/main.go go.mod"; strings.Join(got, " ") != want {
		t.Fatalf("walkFiles = %q, want %q", got, want)
	}
}
