package main

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/httpapi"
	"github.com/krimvp/comitia/pkg/mcp"
)

func main() {
	api, err := httpapi.NewClient("http://127.0.0.1:1", nil)
	if err != nil {
		panic(err)
	}
	_ = mcp.NewHTTPHandler(api, mcp.HTTPConfig{})
	for _, name := range sql.Drivers() {
		if name == "sqlite" {
			fmt.Fprintln(os.Stderr, "remote imports registered the SQLite driver")
			os.Exit(1)
		}
	}
	for _, path := range []string{":memory:", os.Args[1]} {
		store, err := comitia.OpenSQLite(path)
		if store != nil || !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "sqlite.OpenSQLite") {
			fmt.Fprintf(os.Stderr, "missing-driver migration error: store=%v err=%v\n", store, err)
			os.Exit(1)
		}
	}
	if _, err := os.Stat(os.Args[1]); !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "missing-driver open touched database: %v\n", err)
		os.Exit(1)
	}
}
