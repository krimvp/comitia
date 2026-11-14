package sqlstore

import (
	"context"
	"database/sql"

	"github.com/krimvp/comitia/internal/model"
)

// Test access. Production code never uses anything in this file: it exists so
// that tests in other packages can reach the SQL implementation's internals,
// which stay unexported in the files that own them.

// DB returns the store's write connection pool. It exists for tests.
func DB(s *SQLiteStore) *sql.DB { return s.db }

// NewStoreFromDB wraps already opened pools without migrating them. It exists
// for tests that need a store over a database opened with unusual options.
func NewStoreFromDB(db, readers *sql.DB, writers chan struct{}) *SQLiteStore {
	return &SQLiteStore{db: db, readers: readers, writers: writers}
}

// Migrate runs the store's migrations again. It exists for tests.
func Migrate(s *SQLiteStore, ctx context.Context) error { return s.migrate(ctx) }

// MigrationPending reports whether a migration or backfill still has to run.
// It exists for tests.
func MigrationPending(s *SQLiteStore, ctx context.Context) bool { return s.migrationPending(ctx) }

// Querier is what a store transaction runs its statements on. It exists for
// tests that wrap a transaction's statements.
type Querier = querier

// TxStore is the ReadStore and WriteStore of one transaction. It exists for
// tests.
type TxStore = sqlStore

// NewTxStore returns a transaction store running its statements on q. It
// exists for tests.
func NewTxStore(q Querier) *TxStore { return &sqlStore{q: q} }

// TxQuerier returns what a transaction store runs its statements on. It
// exists for tests.
func TxQuerier(s *TxStore) Querier { return s.q }

// TransactionQuerier runs statements in tx under ctx, as a store transaction
// does. It exists for tests.
func TransactionQuerier(tx *sql.Tx, ctx context.Context) Querier { return transactionQuery{tx, ctx} }

// MessageQuery returns the statement and arguments of a message page. It
// exists for tests.
func MessageQuery(cid, sid string, after int64, limit int, f model.MessageFilters) (string, []any) {
	return messageQuery(cid, sid, after, limit, f)
}

// ReferencePageQuery returns the statement and arguments of a reference page.
// It exists for tests.
func ReferencePageQuery(cid, created, afterID string, limit int) (string, []any) {
	return referencePageQuery(cid, created, afterID, limit)
}

// TaskPageQuery returns the statement and arguments of a task page. It exists
// for tests.
func TaskPageQuery(cid string, status model.TaskStatus, f model.TaskFilters, created, afterID string, limit int) (string, []any) {
	return taskPageQuery(cid, status, f, created, afterID, limit)
}

// SchemaGeneration names a schema step in comitia_generations. It exists for
// tests.
func SchemaGeneration(schema string) string { return schemaGeneration(schema) }

// Backfill is a projection backfill. It exists for tests.
type Backfill = backfill

// BackfillGenerations lists the generations that already ran b. It exists for
// tests.
func BackfillGenerations(b Backfill) []string { return b.generations() }

// Migration steps and statements, for tests. Schemas shares its elements with
// the list the store migrates, so a test that changes one changes the next
// migration.
var (
	Schemas          = schemas
	Backfills        = backfills
	CreatorRepairSQL = creatorRepair
)

// Migration statements, for tests.
const (
	SessionSchemaSQL       = sessionSchema
	GrantSchemaSQL         = grantSchema
	MessageHiddenSchemaSQL = messageHiddenSchema
	MessageRepairSQL       = messageRepair
	RevokeGrantTreeSQL     = revokeGrantTreeSQL
	RevokeSessionTreeSQL   = revokeSessionTreeSQL
)
