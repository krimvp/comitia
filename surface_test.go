package comitia_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/importer"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/mcp"
)

var update = flag.Bool("update", false, "rewrite testdata/api_surface.golden from the current tree")

// The public packages whose exported API is frozen: every importable path of
// the module that is not internal or a command. The internal packages behind
// them may change freely as long as this surface does not.
var publicPackages = []string{module, module + "/sqlite", module + "/storetest", module + "/httpapi", module + "/mcp"}

// errorTexts holds the exported error variables of the public packages by
// package path and name. Export data carries no variable values, so the
// surface takes their texts from here: they cross the wire in error messages,
// which clients match. The surface fails on an exported error variable
// missing from this table.
var errorTexts = map[string]error{
	module + ".ErrBootstrapUsed":                           comitia.ErrBootstrapUsed,
	module + ".ErrConflict":                                comitia.ErrConflict,
	module + ".ErrForbidden":                               comitia.ErrForbidden,
	module + ".ErrInvalid":                                 comitia.ErrInvalid,
	module + ".ErrNotFound":                                comitia.ErrNotFound,
	module + ".ErrStaleClaim":                              comitia.ErrStaleClaim,
	module + ".ErrUnauthenticated":                         comitia.ErrUnauthenticated,
	module + "/httpapi.ErrBootstrapOffline":                httpapi.ErrBootstrapOffline,
	module + "/httpapi.ErrClaimRecoveryUnsupported":        httpapi.ErrClaimRecoveryUnsupported,
	module + "/httpapi.ErrReferenceListUnsupported":        httpapi.ErrReferenceListUnsupported,
	module + "/httpapi.ErrResponseTooLarge":                httpapi.ErrResponseTooLarge,
	module + "/httpapi.ErrTaskFiltersUnsupported":          httpapi.ErrTaskFiltersUnsupported,
	module + "/httpapi.ErrTaskMutationReceiptsUnsupported": httpapi.ErrTaskMutationReceiptsUnsupported,
	module + "/httpapi.ErrTaskOutcomesUnsupported":         httpapi.ErrTaskOutcomesUnsupported,
	module + "/httpapi.ErrTaskResultFencingUnsupported":    httpapi.ErrTaskResultFencingUnsupported,
}

// varValues holds the other exported variables of the public packages by
// package path and name; the surface prints each value with %#v. Some cross
// the wire (mcp.SupportedProtocolVersions is offered in the MCP handshake), so
// a new value is a change of contract. The surface fails on an exported
// variable missing from both this table and assignedVars.
var varValues = map[string]any{
	module + "/mcp.ProtocolVersions":          mcp.ProtocolVersions,
	module + "/mcp.SupportedProtocolVersions": mcp.SupportedProtocolVersions,
}

// assignedVars lists the exported variables that programs assign, with the
// reason; the surface pins their types but not their values.
var assignedVars = map[string]string{
	module + ".Version":  "build stamp, set with -ldflags -X or at run time",
	module + ".Revision": "build stamp, set with -ldflags -X or at run time",
	module + ".Dirty":    "build stamp, set with -ldflags -X or at run time",
}

const surfaceGolden = "testdata/api_surface.golden"

func TestPublicAPISurface(t *testing.T) {
	got := apiSurface(t)
	if *update {
		if err := os.WriteFile(surfaceGolden, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(surfaceGolden)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("the exported API of %s changed (- golden, + now):\n%s\n"+
			"If the change is deliberate, run\n\tgo test -run TestPublicAPISurface -update .\n"+
			"and commit %s with it.", strings.Join(publicPackages, ", "), lineDiff(string(want), got), surfaceGolden)
	}
}

// apiSurface lists every exported object of the public packages, read from
// their compiled export data. Types print their underlying type and exported
// method sets, so an alias and the type it names print alike; packages of
// this module print unqualified, so moving a declaration behind an alias does
// not change the surface. Constants print their exact values, error
// variables their texts (from errorTexts) and other variables their values
// (from varValues), except those in assignedVars.
func apiSurface(t *testing.T) string {
	t.Helper()
	cmd := exec.Command("go", append([]string{"list", "-export", "-deps", "-json=ImportPath,Export"}, publicPackages...)...)
	toolchain := runtime.Version()
	if !strings.HasPrefix(toolchain, "go1.") {
		toolchain = "local"
	}
	// Export data must come from the toolchain that built this test.
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN="+toolchain, "GOFLAGS=-buildvcs=false")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list export data: %v", err)
	}
	exports := map[string]string{}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	for {
		var p struct{ ImportPath, Export string }
		if err := dec.Decode(&p); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		exports[p.ImportPath] = p.Export
	}
	imp := importer.ForCompiler(token.NewFileSet(), "gc", func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok || file == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(filepath.Clean(file))
	})
	qualifier := func(p *types.Package) string {
		if p.Path() == module || strings.HasPrefix(p.Path(), module+"/") {
			return ""
		}
		return p.Name()
	}
	var b strings.Builder
	errorVars, valueVars, assigned := 0, 0, 0
	for _, path := range publicPackages {
		pkg, err := imp.Import(path)
		if err != nil {
			t.Fatalf("import %s: %v", path, err)
		}
		var lines []string
		scope := pkg.Scope()
		for _, name := range scope.Names() {
			obj := scope.Lookup(name)
			if !obj.Exported() {
				continue
			}
			line := types.ObjectString(obj, qualifier)
			if c, ok := obj.(*types.Const); ok {
				line += " = " + c.Val().ExactString()
			}
			if v, ok := obj.(*types.Var); ok && types.Identical(v.Type(), errorType) {
				err, ok := errorTexts[path+"."+name]
				if !ok {
					t.Fatalf("%s.%s is an exported error variable missing from errorTexts; add it so the surface pins its text", path, name)
				}
				line += " = " + strconv.Quote(err.Error())
				errorVars++
			} else if ok {
				key := path + "." + name
				if value, pinned := varValues[key]; pinned {
					line += " = " + fmt.Sprintf("%#v", value)
					valueVars++
				} else if _, exempt := assignedVars[key]; exempt {
					assigned++
				} else {
					t.Fatalf("%s.%s is an exported variable missing from varValues; add it so the surface pins its value, or to assignedVars with the reason programs assign it", path, name)
				}
			}
			if tn, ok := obj.(*types.TypeName); ok {
				line = "type " + name + " " + types.TypeString(tn.Type().Underlying(), qualifier)
				for i, typ := range []types.Type{tn.Type(), types.NewPointer(tn.Type())} {
					methods := types.NewMethodSet(typ)
					for j := 0; j < methods.Len(); j++ {
						if f := methods.At(j).Obj(); f.Exported() {
							lines = append(lines, fmt.Sprintf("method %s %s.%s %s", name, []string{"value", "pointer"}[i], f.Name(), types.TypeString(f.Type(), qualifier)))
						}
					}
				}
			}
			lines = append(lines, line)
		}
		sort.Strings(lines)
		fmt.Fprintf(&b, "## %s\n%s\n", path, strings.Join(lines, "\n"))
	}
	if len(errorTexts) != errorVars {
		t.Fatalf("errorTexts names %d error variables, the public packages export %d; remove the stale entries", len(errorTexts), errorVars)
	}
	if len(varValues) != valueVars || len(assignedVars) != assigned {
		t.Fatalf("varValues names %d variables and assignedVars %d, the public packages export %d and %d of them; remove the stale entries", len(varValues), len(assignedVars), valueVars, assigned)
	}
	return b.String()
}

var errorType = types.Universe.Lookup("error").Type()

// lineDiff lists the lines only in want (-) and only in got (+), in order,
// from a longest common subsequence, each under the "## package" header of
// its section.
func lineDiff(want, got string) string {
	a, b := strings.Split(want, "\n"), strings.Split(got, "\n")
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []string
	section, printed := "", ""
	emit := func(line string) {
		if section != printed {
			out = append(out, section)
			printed = section
		}
		out = append(out, line)
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			if strings.HasPrefix(a[i], "## ") {
				section = a[i]
			}
			i, j = i+1, j+1
		case j < len(b) && (i == len(a) || lcs[i][j+1] >= lcs[i+1][j]):
			emit("+ " + b[j])
			j++
		default:
			emit("- " + a[i])
			i++
		}
	}
	return strings.Join(out, "\n")
}
