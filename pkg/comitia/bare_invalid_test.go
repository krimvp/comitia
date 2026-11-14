package comitia_test

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"io/fs"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Explicit inventory of bare ErrInvalid returns that cannot yet be converted:
// none. Walk production sources so newly added sites must be converted or listed.
func TestBareInvalidReturns(t *testing.T) {
	var found []string
	fset := gotoken.NewFileSet()
	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && strings.HasPrefix(entry.Name(), ".") && path != "." {
			return filepath.SkipDir
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
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
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(found)
	var unconverted []string
	if !reflect.DeepEqual(found, unconverted) {
		t.Fatalf("bare ErrInvalid returns (convert or list): %v", found)
	}
}
