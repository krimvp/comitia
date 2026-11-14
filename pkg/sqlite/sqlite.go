// Package sqlite supplies the SQLite driver for embedded Comitia stores.
// Remote clients can import comitia, httpapi and mcp without this package.
package sqlite

import (
	"github.com/krimvp/comitia/pkg/comitia"
	_ "modernc.org/sqlite"
)

// OpenSQLite opens a private SQLite store, migrating it when necessary.
// File databases use WAL and bounded readers; :memory: stores are isolated.
func OpenSQLite(path string) (*comitia.SQLiteStore, error) {
	return comitia.OpenSQLite(path)
}
