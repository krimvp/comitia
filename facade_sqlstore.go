package comitia

import (
	"github.com/krimvp/comitia/internal/sqlstore"
)

type SQLiteStore = sqlstore.SQLiteStore

// OpenSQLite opens a store using an explicitly registered SQLite driver.
//
// Deprecated: use github.com/krimvp/comitia/sqlite.OpenSQLite, which supplies
// the driver. Remote clients importing comitia do not register a driver.
func OpenSQLite(path string) (*SQLiteStore, error) { return sqlstore.OpenSQLite(path) }
