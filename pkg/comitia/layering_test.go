package comitia_test

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const module = "github.com/krimvp/comitia"

// facade is the public API package, the one these tests belong to.
const facade = module + "/pkg/comitia"

// TestInternalLayering checks the packages that go list ./internal/... finds,
// that is the module's internal/ tree (not pkg/httpapi/internal/legacy, which
// is part of the httpapi adapter). It checks two things:
//
//   - production code (non-test files) of an internal package imports only the
//     internal packages its line of the layering table names, and no other
//     package of the module;
//   - tests of an internal package, in-package and external, do not import the
//     public API package.
//
// It does not check test-only imports between internal packages: those are
// allowed. internal/sqlstore's tests import internal/service on purpose, since
// a fixture package shared by both would need an import cycle.
//
// layering lists every internal package with the internal packages its
// production code may import.
const layering = `
internal/model
internal/schema      internal/model
internal/sqlstore    internal/model
internal/service     internal/model internal/schema
internal/mergegate   internal/model internal/schema
internal/evaluation  internal/model internal/schema
internal/httpserve
`

type listedPackage struct {
	ImportPath   string
	Imports      []string
	TestImports  []string
	XTestImports []string
}

func allowedImports() map[string]map[string]bool {
	allowed := map[string]map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(layering), "\n") {
		fields := strings.Fields(line)
		allowed[fields[0]] = map[string]bool{}
		for _, dep := range fields[1:] {
			allowed[fields[0]][dep] = true
		}
	}
	return allowed
}

// layeringViolations names each internal package and the import that breaks
// the table.
func layeringViolations(pkgs []listedPackage) []string {
	allowed := allowedImports()
	var out []string
	for _, p := range pkgs {
		name := strings.TrimPrefix(p.ImportPath, module+"/")
		may, known := allowed[name]
		if !known {
			out = append(out, fmt.Sprintf("%s is not in the layering table; add it with the internal packages it may import", name))
			continue
		}
		for _, imp := range p.Imports {
			if imp == facade {
				out = append(out, fmt.Sprintf("%s imports the public API package %s; internal packages must not depend on the public API", name, facade))
				continue
			}
			dep, ok := strings.CutPrefix(imp, module+"/")
			if ok && !may[dep] {
				out = append(out, fmt.Sprintf("%s imports %s, which the layering table does not allow (allowed: %s)", name, dep, strings.Join(fields(may), ", ")))
			}
		}
		for _, imp := range append(append([]string{}, p.TestImports...), p.XTestImports...) {
			if imp == facade {
				out = append(out, fmt.Sprintf("tests of %s import the public API package %s; internal packages must not depend on the public API", name, facade))
			}
		}
	}
	return out
}

func fields(set map[string]bool) []string {
	if len(set) == 0 {
		return []string{"none"}
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// moduleRoot returns the directory that holds go.mod. Tests run in
// pkg/comitia, so patterns and paths of the whole module are resolved from
// here rather than from the working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("go", "env", "GOMOD")
	cmd.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		t.Fatalf("not inside a module: go env GOMOD printed %q", gomod)
	}
	return filepath.Dir(gomod)
}

// listPackages returns the packages that go list finds for patterns, relative
// to the module root, with their direct imports.
func listPackages(t *testing.T, patterns ...string) []listedPackage {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list", "-json=ImportPath,Imports,TestImports,XTestImports"}, patterns...)...)
	cmd.Dir = moduleRoot(t)
	cmd.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list %s: %v", strings.Join(patterns, " "), err)
	}
	var pkgs []listedPackage
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p listedPackage
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

func TestInternalLayering(t *testing.T) {
	pkgs := listPackages(t, "./internal/...")
	if len(pkgs) < len(allowedImports()) {
		t.Fatalf("listed %d internal packages, the table names %d", len(pkgs), len(allowedImports()))
	}
	for _, v := range layeringViolations(pkgs) {
		t.Error(v)
	}
}

// The adapters cmd/comitia, pkg/mcp and pkg/httpapi reach the implementation
// only through the public API package and pkg/sqlite: no package under them,
// production code or tests, imports a package of the module's internal/ tree
// directly. Only direct imports are checked; the public API package and
// pkg/sqlite import internal packages, so the adapters depend on them
// indirectly. Every other package under pkg/ (pkg/sqlite, pkg/storetest and
// any added later) is held to the same rule, since its exports are public
// API; the facade, pkg/comitia, is the one package under pkg/ that imports
// the implementation. The examples are held to it too, since they show the
// public API.
var adapterPatterns = []string{"./cmd/...", "./pkg/...", "./examples/..."}

// adapterMayImport lists the internal packages the adapters may nevertheless
// import directly, one per line: internal packages meant for the adapters.
// internal/httpserve holds the HTTP listener code cmd/comitia and pkg/mcp share.
var adapterMayImport = map[string]bool{
	"internal/httpserve": true,
}

// adapterViolations names each adapter package and the internal package it
// imports directly without adapterMayImport allowing it.
func adapterViolations(pkgs []listedPackage) []string {
	var out []string
	for _, p := range pkgs {
		name := strings.TrimPrefix(p.ImportPath, module+"/")
		for _, imports := range []struct {
			who  string
			list []string
		}{{name + " imports", p.Imports}, {"tests of " + name + " import", p.TestImports}, {"tests of " + name + " import", p.XTestImports}} {
			for _, imp := range imports.list {
				dep, ok := strings.CutPrefix(imp, module+"/")
				if ok && strings.HasPrefix(dep, "internal/") && !adapterMayImport[dep] {
					out = append(out, fmt.Sprintf("%s %s; adapters reach the implementation only through the public API package and pkg/sqlite (or add it to adapterMayImport)", imports.who, dep))
				}
			}
		}
	}
	return out
}

func TestAdapterImports(t *testing.T) {
	var pkgs []listedPackage
	listed := map[string]bool{}
	for _, p := range listPackages(t, adapterPatterns...) {
		listed[p.ImportPath] = true
		if p.ImportPath != facade {
			pkgs = append(pkgs, p)
		}
	}
	for _, adapter := range []string{"cmd/comitia", "pkg/mcp", "pkg/httpapi", "pkg/httpapi/internal/legacy", "pkg/sqlite", "pkg/storetest", "examples/embedded"} {
		if !listed[module+"/"+adapter] {
			t.Fatalf("adapter %s not listed", adapter)
		}
	}
	for _, v := range adapterViolations(pkgs) {
		t.Error(v)
	}
}

func TestAdapterImportsRejectViolations(t *testing.T) {
	got := adapterViolations([]listedPackage{
		{ImportPath: module + "/pkg/mcp", Imports: []string{facade, module + "/internal/model"}},
		{ImportPath: module + "/pkg/httpapi", TestImports: []string{module + "/pkg/httpapi/internal/legacy", module + "/pkg/sqlite"}, XTestImports: []string{module + "/internal/service"}},
		{ImportPath: module + "/cmd/comitia", Imports: []string{module + "/pkg/httpapi", module + "/pkg/mcp", module + "/pkg/sqlite"}},
	})
	want := []string{
		"pkg/mcp imports internal/model; adapters reach the implementation only through the public API package and pkg/sqlite (or add it to adapterMayImport)",
		"tests of pkg/httpapi import internal/service; adapters reach the implementation only through the public API package and pkg/sqlite (or add it to adapterMayImport)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("violations:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The check itself must catch a forbidden import, an unknown package and an
// import of the public API package.
func TestInternalLayeringRejectsViolations(t *testing.T) {
	got := layeringViolations([]listedPackage{
		{ImportPath: module + "/internal/model", Imports: []string{"errors", module + "/internal/schema"}},
		{ImportPath: module + "/internal/service", Imports: []string{module + "/internal/model", module + "/internal/sqlstore", facade}},
		{ImportPath: module + "/internal/extra"},
		{ImportPath: module + "/internal/schema", XTestImports: []string{facade}},
	})
	want := []string{
		"internal/model imports internal/schema, which the layering table does not allow (allowed: none)",
		"internal/service imports internal/sqlstore, which the layering table does not allow (allowed: internal/model, internal/schema)",
		"internal/service imports the public API package github.com/krimvp/comitia/pkg/comitia; internal packages must not depend on the public API",
		"internal/extra is not in the layering table; add it with the internal packages it may import",
		"tests of internal/schema import the public API package github.com/krimvp/comitia/pkg/comitia; internal packages must not depend on the public API",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("violations:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// goDirectories are the top-level directories that may hold Go files, each
// with what it is for. go list ./... finds packages in these alone.
var goDirectories = map[string]string{
	"cmd":      "the commands: cmd/comitia",
	"examples": "programs that show the public API, compiled by the tests",
	"internal": "the implementation, importable only from inside this module",
	"pkg":      "the packages other modules import",
}

// The repository root holds the module files and the documents a reader looks
// for first (README.md, CONTRIBUTING.md, ...); Go code lives only under
// goDirectories. A Go file at the root would be a package at the module path,
// beside the public API in pkg/comitia, and one in a new top-level directory
// (client/) a public import path outside pkg/. The files checked are those of
// repositoryFiles, so an ignored leftover such as httpapi/ in a clone from
// before the move to pkg/ does not count, and a new directory not yet added
// to git does.
func TestGoCodeStaysInItsDirectories(t *testing.T) {
	_, files := repositoryFiles(t)
	if len(files) == 0 {
		t.Fatal("no repository files listed")
	}
	for _, v := range goPlacementViolations(files) {
		t.Error(v)
	}
}

// goPlacementViolations names each Go file outside goDirectories, a testdata
// directory at the root, and each entry of goDirectories that holds no Go
// file, given module-relative file paths.
func goPlacementViolations(files []string) []string {
	var out []string
	holds := map[string]bool{}
	rootTestdata := false
	for _, rel := range files {
		top, _, nested := strings.Cut(rel, "/")
		if nested && top == "testdata" && !rootTestdata {
			rootTestdata = true
			out = append(out, "testdata/ is at the repository root; test data belongs beside the package that reads it")
		}
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		if _, ok := goDirectories[top]; nested && ok {
			holds[top] = true
			continue
		}
		out = append(out, fmt.Sprintf("%s is outside %s; put Go code in a package under one of them (or, deliberately, add a directory to goDirectories)", rel, strings.Join(goDirectoryNames(), ", ")))
	}
	for _, dir := range goDirectoryNames() {
		if !holds[strings.TrimSuffix(dir, "/")] {
			out = append(out, fmt.Sprintf("%s holds no Go file; remove it from goDirectories", dir))
		}
	}
	return out
}

func goDirectoryNames() []string {
	var names []string
	for dir := range goDirectories {
		names = append(names, dir+"/")
	}
	sort.Strings(names)
	return names
}

func TestGoPlacementRejectsViolations(t *testing.T) {
	got := goPlacementViolations([]string{
		"README.md", "go.mod", "zz.go", "client/client.go", ".github/x.go", "scripts/x.py",
		"cmd/comitia/main.go", "internal/model/model.go", "pkg/comitia/api.go",
		"testdata/a.json", "testdata/b.json", "internal/service/testdata/x.go",
	})
	want := []string{
		"zz.go is outside cmd/, examples/, internal/, pkg/; put Go code in a package under one of them (or, deliberately, add a directory to goDirectories)",
		"client/client.go is outside cmd/, examples/, internal/, pkg/; put Go code in a package under one of them (or, deliberately, add a directory to goDirectories)",
		".github/x.go is outside cmd/, examples/, internal/, pkg/; put Go code in a package under one of them (or, deliberately, add a directory to goDirectories)",
		"testdata/ is at the repository root; test data belongs beside the package that reads it",
		"examples/ holds no Go file; remove it from goDirectories",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("violations:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
