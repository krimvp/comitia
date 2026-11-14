package comitia_test

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
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
// developer's machine ignored leftovers are not seen (bin/, dist/,
// node_modules/, or the ignored httpapi/ui-tests/browser/node_modules/ an old
// clone keeps after the move to pkg/), but a file that is new and not yet
// added is, so a new directory fails here before it is committed rather than
// first in CI. Keep a scratch file out of the guards with .git/info/exclude.
//
// When git cannot list the files, the tree is walked instead (walkFiles), and
// the test log says why: the tree is not a git repository or not its own one
// (a source archive), where the walk is the right answer, or git is missing
// or refused (for example a repository owned by another user), where it is a
// fallback.
func repositoryFiles(t *testing.T) (string, []string) {
	t.Helper()
	root := moduleRoot(t)
	files, why := gitFiles(t, root)
	if why != "" {
		t.Logf("listing files by walking %s, honouring its .gitignore, since %s", root, why)
		files = walkFiles(t, root)
	}
	sort.Strings(files)
	return root, files
}

// gitFiles lists the files with git, or returns why it could not.
func gitFiles(t *testing.T, root string) ([]string, string) {
	t.Helper()
	top, err := gitOutput(root, "rev-parse", "--show-toplevel")
	var exit *exec.ExitError
	switch {
	case errors.Is(err, exec.ErrNotFound):
		return nil, fmt.Sprintf("git is not installed or not on PATH (%v)", err)
	case errors.As(err, &exit) && bytes.Contains(exit.Stderr, []byte("not a git repository")):
		return nil, "the tree is not a git repository"
	case err != nil:
		return nil, fmt.Sprintf("git refused: %v: %s", err, bytes.TrimSpace(stderrOf(err)))
	}
	if dir := strings.TrimSpace(string(top)); !sameDir(dir, root) {
		return nil, fmt.Sprintf("the tree is inside the git repository at %s, not its own", dir)
	}
	out, err := gitOutput(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		t.Fatalf("git ls-files: %v: %s", err, stderrOf(err))
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
	return files, ""
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Output()
}

func stderrOf(err error) []byte {
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.Stderr
	}
	return nil
}

func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// walkFiles lists every file under root that the root .gitignore does not
// exclude, always skipping .git, node_modules and __pycache__ directories.
// It reads only the root .gitignore, not those of subdirectories,
// .git/info/exclude or a global excludes file, and does not understand "**"
// or backslash escapes; the root .gitignore of this repository uses neither,
// and the nested ones only repeat node_modules/. As in git, a file inside an
// excluded directory cannot be included again.
func walkFiles(t *testing.T, root string) []string {
	t.Helper()
	rules, err := readIgnoreRules(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "__pycache__":
				return filepath.SkipDir
			}
			if ignored(rules, rel, true) {
				return filepath.SkipDir
			}
			return nil
		}
		if !ignored(rules, rel, false) {
			files = append(files, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

type ignoreRule struct {
	pattern                   string
	negate, dirOnly, anchored bool
}

// readIgnoreRules reads the patterns of a .gitignore file; a missing file
// has none.
func readIgnoreRules(file string) ([]ignoreRule, error) {
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var rules []ignoreRule
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, " \t\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var r ignoreRule
		if rest, ok := strings.CutPrefix(line, "!"); ok {
			r.negate, line = true, rest
		}
		if rest, ok := strings.CutSuffix(line, "/"); ok {
			r.dirOnly, line = true, rest
		}
		// A slash at the start or in the middle anchors the pattern to the
		// directory of the .gitignore; otherwise it matches a name at any depth.
		r.anchored = strings.Contains(line, "/")
		r.pattern = strings.TrimPrefix(line, "/")
		rules = append(rules, r)
	}
	return rules, nil
}

// ignored applies the rules in order, the last match deciding, as git does.
func ignored(rules []ignoreRule, rel string, dir bool) bool {
	out := false
	for _, r := range rules {
		if r.dirOnly && !dir {
			continue
		}
		name := path.Base(rel)
		if r.anchored {
			name = rel
		}
		if ok, _ := path.Match(r.pattern, name); ok {
			out = !r.negate
		}
	}
	return out
}

// TestRepositoryFilesWithoutGit checks the walk on a small tree: it must
// list the files, skip the directories named above and honour the root
// .gitignore, including a negation and anchored and directory patterns.
func TestRepositoryFilesWithoutGit(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		".gitignore":          "# build output\n/bin/\n/comitia\n*.db\n!keep.db\ndocs/*.tmp\n",
		"go.mod":              "",
		"cmd/x/main.go":       "",
		"cmd/comitia/main.go": "",
		"node_modules/m/m.go": "",
		"a/__pycache__/c.pyc": "",
		".git/HEAD":           "",
		"bin/comitia":         "",
		"comitia":             "",
		"pkg/bin/x.go":        "",
		"state.db":            "",
		"data/keep.db":        "",
		"docs/a.tmp":          "",
		"docs/sub/b.tmp":      "",
	}
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got := walkFiles(t, root)
	sort.Strings(got)
	want := ".gitignore cmd/comitia/main.go cmd/x/main.go data/keep.db docs/sub/b.tmp go.mod pkg/bin/x.go"
	if strings.Join(got, " ") != want {
		t.Fatalf("walkFiles = %q, want %q", strings.Join(got, " "), want)
	}
}
