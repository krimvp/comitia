package comitia

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
	mu sync.Mutex
}

func OpenSQLite(path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: sqlite path", ErrInvalid)
	}
	absPath, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	// SQLite's busy handler polls rather than queues, so under load one CLI
	// process can lose the write lock to others for seconds; bound that wait
	// generously instead of failing with SQLITE_BUSY.
	dsn := (&url.URL{Scheme: "file", Path: absPath}).String() + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(15000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	if path == ":memory:" {
		dsn = "file:comitia-memory-" + id() + "?mode=memory&cache=shared&_pragma=foreign_keys(1)&_txlock=immediate"
	}
	db, e := sql.Open("sqlite", dsn)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db}
	if e = s.migrate(context.Background()); e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func (s *SQLiteStore) Close() error { return s.db.Close() }
func (s *SQLiteStore) migrate(ctx context.Context) error {
	// Every CLI call opens the store. Backfills take the write lock and scan
	// whole tables, so probe lock free (WAL readers do not block) and take the
	// write lock only when an object is missing or a backfill has rows to add,
	// e.g. after an older binary wrote. busy_timeout applies to BEGIN IMMEDIATE.
	if !s.migrationPending(ctx) {
		return nil
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	for _, schema := range schemas {
		if _, e = tx.ExecContext(ctx, schema); e != nil {
			return e
		}
	}
	return tx.Commit()
}

var (
	schemas    = []string{sessionSchema, contextSchema, grantSchema, messageSchema, taskSchema, contextTreeSchema, contextLifecycleSchema}
	createStmt = regexp.MustCompile(`(?is)^CREATE\s+(?:UNIQUE\s+)?(TABLE|INDEX|TRIGGER|VIEW)\s+IF\s+NOT\s+EXISTS\s+(\w+)`)
	insertStmt = regexp.MustCompile(`(?is)^INSERT\s+(?:OR\s+IGNORE\s+)?INTO\s+(\w+)\s+(SELECT\s.*)$`)
)

// migrationPending reports whether any schema statement would change the
// database. A backfill is pending when its SELECT yields rows the target lacks;
// unrecognised statements, and probe errors such as missing tables, count as
// pending.
func (s *SQLiteStore) migrationPending(ctx context.Context) bool {
	for _, schema := range schemas {
		for _, stmt := range strings.Split(schema, ";") {
			stmt = strings.TrimSpace(stmt)
			probe, args := "", []any(nil)
			if m := createStmt.FindStringSubmatch(stmt); m != nil {
				probe, args = "SELECT NOT EXISTS(SELECT 1 FROM sqlite_master WHERE type=? AND name=?)", []any{strings.ToLower(m[1]), m[2]}
			} else if m := insertStmt.FindStringSubmatch(stmt); m != nil {
				probe = "SELECT EXISTS(SELECT * FROM (" + m[2] + ") EXCEPT SELECT * FROM " + m[1] + ")"
			} else if stmt != "" {
				return true
			} else {
				continue
			}
			var pending bool
			if e := s.db.QueryRowContext(ctx, probe, args...).Scan(&pending); e != nil || pending {
				return true
			}
		}
	}
	return false
}
func (s *SQLiteStore) View(ctx context.Context, fn func(ReadStore) error) error {
	tx, e := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	v := &sqlStore{q: tx}
	if e = fn(v); e != nil {
		tx.Rollback()
		return e
	}
	return tx.Commit()
}
func (s *SQLiteStore) Update(ctx context.Context, fn func(WriteStore) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	v := &sqlStore{q: tx}
	if e = fn(v); e != nil {
		tx.Rollback()
		return e
	}
	return tx.Commit()
}

type querier interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}
type sqlStore struct{ q querier }

func ts(t time.Time) string    { return t.UTC().Format(time.RFC3339Nano) }
func parse(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }
func nullTime(n sql.NullString) *time.Time {
	if !n.Valid {
		return nil
	}
	t := parse(n.String)
	return &t
}
func dbErr(e error) error {
	if errors.Is(e, sql.ErrNoRows) {
		return ErrNotFound
	}
	return e
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func timeString(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}
