package comitia_test

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// bareInvalidAreas are directories the scan must have parsed production
// files from, whatever go list reports: the adapters, the service, the frozen
// fixtures and the test programs under testdata, which go list leaves out.
var bareInvalidAreas = []string{
	"cmd/comitia",
	"examples/embedded",
	"internal/model",
	"internal/service",
	"internal/service/testdata",
	"pkg/comitia",
	"pkg/comitia/testdata/remote-import",
	"pkg/httpapi",
	"pkg/httpapi/internal/legacy",
	"pkg/mcp",
	"pkg/sqlite",
	"pkg/storetest",
}

// Explicit inventory of bare ErrInvalid returns that cannot yet be converted:
// none. The scan parses every production (non-test) Go file of the
// repository (see repositoryFiles), testdata and the frozen fixtures
// included, so newly added sites must be converted or listed. Positions are
// module-relative.
func TestBareInvalidReturns(t *testing.T) {
	root, files := repositoryFiles(t)
	var found []string
	parsed := map[string]bool{}
	fset := gotoken.NewFileSet()
	for _, rel := range files {
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, rel, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		parsed[rel] = true
		parsed[path.Dir(rel)] = true
		ast.Inspect(file, func(n ast.Node) bool {
			r, ok := n.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			for _, expr := range r.Results {
				name := ""
				switch x := expr.(type) {
				case *ast.Ident:
					name = x.Name
				case *ast.SelectorExpr:
					name = x.Sel.Name
				}
				if name == "ErrInvalid" {
					found = append(found, fset.Position(expr.Pos()).String())
				}
			}
			return true
		})
	}

	// The scan must not pass by parsing too little: it covers every file
	// go build ./... compiles and the areas named above.
	built := builtGoFiles(t, root)
	if len(built) == 0 {
		t.Fatal("go list ./... reported no Go files")
	}
	for _, rel := range built {
		if !parsed[rel] {
			t.Errorf("the scan did not parse %s, which go list ./... builds", rel)
		}
	}
	for _, dir := range bareInvalidAreas {
		if !parsed[dir] {
			t.Errorf("the scan parsed no production Go file in %s", dir)
		}
	}

	sort.Strings(found)
	var unconverted []string
	if !reflect.DeepEqual(found, unconverted) {
		t.Fatalf("bare ErrInvalid returns (convert or list): %v", found)
	}
}

// builtGoFiles lists the module-relative paths of the non-test Go files of
// every package go list ./... finds from the module root.
func builtGoFiles(t *testing.T, root string) []string {
	t.Helper()
	cmd := exec.Command("go", "list", "-f", "{{$d := .Dir}}{{range .GoFiles}}{{$d}}/{{.}}\n{{end}}{{range .CgoFiles}}{{$d}}/{{.}}\n{{end}}", "./...")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list ./...: %v", err)
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		if line == "" {
			continue
		}
		rel, err := filepath.Rel(root, line)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, filepath.ToSlash(rel))
	}
	return files
}
