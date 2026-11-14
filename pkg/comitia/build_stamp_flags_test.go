package comitia_test

import (
	"encoding/json"
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

// stampFlagFiles must pass the stamp to the build or pin it in a test; each
// is checked to contain at least one flag, so the scan below cannot pass by
// finding none.
var stampFlagFiles = []string{".github/workflows/ci.yml", "scripts/comitia_install.py", "scripts/test_install_credentials.py", "CONTRIBUTING.md"}

// ciStampFlags is the build stamp of ci.yml's release build, written out in
// full with this package's path: the workflow runs nowhere else, and a path
// held in a shell variable would escape the scan.
func ciStampFlags(stampPackage string) []string {
	return []string{"-X " + stampPackage + ".Revision=$COMITIA_REVISION", "-X " + stampPackage + ".Dirty=$COMITIA_DIRTY"}
}

// A -X flag naming the wrong package is ignored by the linker without an
// error, so a stale path only shows as a binary without a version (in a git
// checkout the revision still comes from the VCS stamp, hiding it further).
// Every place in the repository that passes or documents the flag must name
// this package, and a binary built with that path must report the stamp.
//
// The scan reads the files of repositoryFiles (so ignored files such as
// dist/ are not read), at any size, except the dated records under
// docs/history and docs/validation, which keep the commands of their time.
// It finds a flag written out as one string, github.com/krimvp/comitia...
// .Version=, .Revision= or .Dirty=. It does not see a path held in a
// variable, joined from parts or split across quotes; it parses no shell or
// Python. The build paths of this repository are pinned beyond the scan:
// ci.yml must hold ciStampFlags verbatim, and scripts/test_install_credentials.py
// checks the whole flag string scripts/comitia_install.py builds.
func TestBuildStampFlagsNameThisPackage(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}}", ".").Output()
	if err != nil {
		t.Fatalf("go list .: %v", err)
	}
	stampPackage := strings.TrimSpace(string(out))
	if stampPackage != facade {
		t.Fatalf("the build stamp variables are in %s, the tests expect %s", stampPackage, facade)
	}

	root, files := repositoryFiles(t)
	found := map[string]int{}
	for _, rel := range files {
		if strings.HasPrefix(rel, "docs/history/") || strings.HasPrefix(rel, "docs/validation/") {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(rel))
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range stampFlag.FindAllStringSubmatch(string(raw), -1) {
			found[rel]++
			if m[1] != stampPackage {
				t.Errorf("%s: %s names %s; the build stamp variables are in %s", rel, m[0], m[1], stampPackage)
			}
		}
	}
	for _, file := range stampFlagFiles {
		if found[file] == 0 {
			t.Errorf("%s passes no build stamp flag; the scan expects one there", file)
		}
	}
	ci, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range ciStampFlags(stampPackage) {
		if !strings.Contains(string(ci), flag) {
			t.Errorf("ci.yml does not pass %q to its release build; write the flag out in full", flag)
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
