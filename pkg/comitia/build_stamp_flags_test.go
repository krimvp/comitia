package comitia_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
)

// stampFlag finds a linker flag target for the build stamp variables in any
// file: the package path before .Version=, .Revision= or .Dirty=.
var stampFlag = regexp.MustCompile(`(github\.com/krimvp/comitia(?:/[A-Za-z0-9_.-]+)*)\.(Version|Revision|Dirty)=`)

// stampFlagFiles must pass the stamp to the build; each is checked to contain
// at least one flag, so the scan below cannot pass by finding none.
var stampFlagFiles = []string{".github/workflows/ci.yml", "scripts/comitia_install.py", "CONTRIBUTING.md"}

// A -X flag naming the wrong package is ignored by the linker without an
// error, so a stale path only shows as a binary without a version. Every
// place in the repository that passes or documents the flag must name this
// package, and a binary built with that path must report the stamp. Dated
// records under docs/history and docs/validation keep the commands of their
// time and are not scanned.
func TestBuildStampFlagsNameThisPackage(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatalf("go list .: %v", err)
	}
	stampPackage := strings.TrimSpace(string(out))
	if stampPackage != facade {
		t.Fatalf("the build stamp variables are in %s, the tests expect %s", stampPackage, facade)
	}

	root := moduleRoot(t)
	found := map[string]int{}
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch {
			case d.Name() == ".git" || d.Name() == "node_modules" || d.Name() == "__pycache__":
				return filepath.SkipDir
			case rel == "docs/history" || rel == "docs/validation":
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 4<<20 {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range stampFlag.FindAllStringSubmatch(string(raw), -1) {
			found[rel]++
			if m[1] != stampPackage {
				t.Errorf("%s: %s names %s; the build stamp variables are in %s", rel, m[0], m[1], stampPackage)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range stampFlagFiles {
		if found[file] == 0 {
			t.Errorf("%s passes no build stamp flag; the scan expects one there", file)
		}
	}

	if testing.Short() {
		t.Skip("building the binary")
	}
	binary := filepath.Join(t.TempDir(), "comitia")
	want := comitia.BuildInfo{Version: "v0.0.0-stamp-test", Revision: strings.Repeat("ab", 20), Dirty: "true"}
	ldflags := "-X " + stampPackage + ".Version=" + want.Version + " -X " + stampPackage + ".Revision=" + want.Revision + " -X " + stampPackage + ".Dirty=" + want.Dirty
	build := exec.Command("go", "build", "-ldflags", ldflags, "-o", binary, "./cmd/comitia")
	build.Dir = root
	build.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	out, err = exec.Command(binary, "version").Output()
	if err != nil {
		t.Fatalf("comitia version: %v", err)
	}
	var got comitia.BuildInfo
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("comitia version printed %q: %v", out, err)
	}
	if got != want {
		t.Fatalf("comitia version = %+v, want %+v from -ldflags %q", got, want, ldflags)
	}
}
