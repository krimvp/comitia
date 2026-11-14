package comitia_test

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
)

const module = "github.com/krimvp/comitia"

// TestInternalLayering checks the packages that go list ./internal/... finds,
// that is the module's internal/ tree (not httpapi/internal/legacy, which is
// part of the httpapi adapter). It checks two things:
//
//   - production code (non-test files) of an internal package imports only the
//     internal packages its line of the layering table names, and no other
//     package of the module;
//   - tests of an internal package, in-package and external, do not import the
//     root package.
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
			if imp == module {
				out = append(out, fmt.Sprintf("%s imports the root package %s; internal packages must not depend on the public API", name, module))
				continue
			}
			dep, ok := strings.CutPrefix(imp, module+"/")
			if ok && !may[dep] {
				out = append(out, fmt.Sprintf("%s imports %s, which the layering table does not allow (allowed: %s)", name, dep, strings.Join(fields(may), ", ")))
			}
		}
		for _, imp := range append(append([]string{}, p.TestImports...), p.XTestImports...) {
			if imp == module {
				out = append(out, fmt.Sprintf("tests of %s import the root package %s; internal packages must not depend on the public API", name, module))
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

// listPackages returns the packages that go list finds for patterns, with
// their direct imports.
func listPackages(t *testing.T, patterns ...string) []listedPackage {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list", "-json=ImportPath,Imports,TestImports,XTestImports"}, patterns...)...)
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

// The adapters cmd/comitia, mcp and httpapi reach the implementation only
// through the root package and sqlite: no package under them, production code
// or tests, imports a package of the module's internal/ tree directly. Only
// direct imports are checked; the root and sqlite import internal packages,
// so the adapters depend on them indirectly.
var adapterPatterns = []string{"./cmd/...", "./mcp/...", "./httpapi/..."}

// adapterMayImport lists the internal packages the adapters may nevertheless
// import directly, one per line. It is empty today. An internal package meant
// for the adapters, such as a planned internal/httpserve shared by cmd/comitia
// and mcp, is added here as
//
//	"internal/httpserve": true,
var adapterMayImport = map[string]bool{}

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
					out = append(out, fmt.Sprintf("%s %s; adapters reach the implementation only through the root package and sqlite (or add it to adapterMayImport)", imports.who, dep))
				}
			}
		}
	}
	return out
}

func TestAdapterImports(t *testing.T) {
	pkgs := listPackages(t, adapterPatterns...)
	listed := map[string]bool{}
	for _, p := range pkgs {
		listed[p.ImportPath] = true
	}
	for _, adapter := range []string{"cmd/comitia", "mcp", "httpapi"} {
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
		{ImportPath: module + "/mcp", Imports: []string{module, module + "/internal/model"}},
		{ImportPath: module + "/httpapi", TestImports: []string{module + "/httpapi/internal/legacy", module + "/sqlite"}, XTestImports: []string{module + "/internal/service"}},
		{ImportPath: module + "/cmd/comitia", Imports: []string{module + "/httpapi", module + "/mcp", module + "/sqlite"}},
	})
	want := []string{
		"mcp imports internal/model; adapters reach the implementation only through the root package and sqlite (or add it to adapterMayImport)",
		"tests of httpapi import internal/service; adapters reach the implementation only through the root package and sqlite (or add it to adapterMayImport)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("violations:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The check itself must catch a forbidden import, an unknown package and an
// import of the root.
func TestInternalLayeringRejectsViolations(t *testing.T) {
	got := layeringViolations([]listedPackage{
		{ImportPath: module + "/internal/model", Imports: []string{"errors", module + "/internal/schema"}},
		{ImportPath: module + "/internal/service", Imports: []string{module + "/internal/model", module + "/internal/sqlstore", module}},
		{ImportPath: module + "/internal/extra"},
		{ImportPath: module + "/internal/schema", XTestImports: []string{module}},
	})
	want := []string{
		"internal/model imports internal/schema, which the layering table does not allow (allowed: none)",
		"internal/service imports internal/sqlstore, which the layering table does not allow (allowed: internal/model, internal/schema)",
		"internal/service imports the root package github.com/krimvp/comitia; internal packages must not depend on the public API",
		"internal/extra is not in the layering table; add it with the internal packages it may import",
		"tests of internal/schema import the root package github.com/krimvp/comitia; internal packages must not depend on the public API",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("violations:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
