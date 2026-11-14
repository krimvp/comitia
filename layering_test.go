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

// layering lists every internal package with the internal packages its
// production code may import. Nothing else of the module may be imported, and
// no internal package, tests included, may import the root package.
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

func TestInternalLayering(t *testing.T) {
	cmd := exec.Command("go", "list", "-json", "./internal/...")
	cmd.Env = append(os.Environ(), "GOFLAGS=-buildvcs=false")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list internal packages: %v", err)
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
	if len(pkgs) < len(allowedImports()) {
		t.Fatalf("listed %d internal packages, the table names %d", len(pkgs), len(allowedImports()))
	}
	for _, v := range layeringViolations(pkgs) {
		t.Error(v)
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
