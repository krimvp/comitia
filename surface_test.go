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
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/api_surface.golden from the current tree")

// The public packages whose exported API is frozen. The internal packages
// behind them may change freely as long as this surface does not.
var publicPackages = []string{module, module + "/sqlite", module + "/storetest"}

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
// not change the surface.
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
	return b.String()
}

// lineDiff lists the lines only in want (-) and only in got (+), in order,
// from a longest common subsequence.
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
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			i, j = i+1, j+1
		case j < len(b) && (i == len(a) || lcs[i][j+1] >= lcs[i+1][j]):
			out = append(out, "+ "+b[j])
			j++
		default:
			out = append(out, "- "+a[i])
			i++
		}
	}
	return strings.Join(out, "\n")
}
