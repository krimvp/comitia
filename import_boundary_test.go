package comitia_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteImportsExcludeSQLiteDriver(t *testing.T) {
	for _, pkg := range []string{"github.com/krimvp/comitia", "github.com/krimvp/comitia/httpapi", "github.com/krimvp/comitia/mcp"} {
		t.Run(pkg, func(t *testing.T) {
			cmd := exec.Command("go", "list", "-deps", pkg)
			cmd.Env = append(os.Environ(), "GOTOOLCHAIN=go1.24.13", "GOFLAGS=-buildvcs=false")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("list dependencies: %v\n%s", err, out)
			}
			for _, dep := range strings.Fields(string(out)) {
				if strings.HasPrefix(dep, "modernc.org/") {
					t.Errorf("remote import %s includes %s", pkg, dep)
				}
			}
		})
	}
}

func TestRemoteImportsDoNotRegisterSQLite(t *testing.T) {
	cmd := exec.Command("go", "run", "./testdata/remote-import", filepath.Join(t.TempDir(), "state.db"))
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=go1.24.13", "GOFLAGS=-buildvcs=false")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("remote process: %v\n%s", err, out)
	}
}
