package comitia

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
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
	dsn := (&url.URL{Scheme: "file", Path: absPath}).String() + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"
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
	for _, schema := range []string{sessionSchema, contextSchema, grantSchema, messageSchema, taskSchema} {
		if _, e := s.db.ExecContext(ctx, schema); e != nil {
			return e
		}
	}
	return nil
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
