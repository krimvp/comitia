package comitia_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/krimvp/comitia/pkg/sqlite"
)

func TestRemoteImportsExcludeSQLiteDriver(t *testing.T) {
	for _, pkg := range []string{"github.com/krimvp/comitia/pkg/comitia", "github.com/krimvp/comitia/pkg/httpapi", "github.com/krimvp/comitia/pkg/mcp"} {
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

// The program also checks that comitia.OpenSQLite, without a driver, sends
// the caller to the function that supplies one, by its import path. The path
// comes from the function itself, so the message cannot keep a stale one.
func TestRemoteImportsDoNotRegisterSQLite(t *testing.T) {
	openSQLite := runtime.FuncForPC(reflect.ValueOf(sqlite.OpenSQLite).Pointer()).Name()
	if !strings.HasPrefix(openSQLite, module+"/") || !strings.HasSuffix(openSQLite, "/sqlite.OpenSQLite") {
		t.Fatalf("sqlite.OpenSQLite is named %s", openSQLite)
	}
	cmd := exec.Command("go", "run", "./testdata/remote-import", filepath.Join(t.TempDir(), "state.db"), openSQLite)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=go1.24.13", "GOFLAGS=-buildvcs=false")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("remote process: %v\n%s", err, out)
	}
}
