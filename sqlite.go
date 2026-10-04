package comitia

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type SQLiteStore struct {
	db      *sql.DB
	readers *sql.DB
	writers chan struct{}
}

const sqliteReaderLimit = 4

func OpenSQLite(path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, fmt.Errorf("%w: sqlite path", ErrInvalid)
	}
	absPath, e := filepath.Abs(path)
	if e != nil {
		return nil, e
	}
	if path != ":memory:" {
		if e := privateSQLiteFiles(absPath); e != nil {
			return nil, e
		}
	}
	// Short busy waits let admission check cancellation between attempts.
	// begin retains the overall contention budget for separate CLI processes.
	dsn := (&url.URL{Scheme: "file", Path: absPath}).String() + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(10)&_pragma=journal_mode(WAL)&_txlock=immediate"
	if path == ":memory:" {
		dsn = "file:comitia-memory-" + id() + "?mode=memory&cache=shared&_pragma=foreign_keys(1)&_pragma=busy_timeout(10)&_txlock=immediate"
	}
	db, e := sql.Open("sqlite", dsn)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db, writers: make(chan struct{}, 1)}
	if e = s.migrate(context.Background()); e != nil {
		db.Close()
		return nil, e
	}
	if path == ":memory:" {
		// Shared-cache memory databases cannot use WAL. Keep their transactions
		// serialized rather than introducing table-lock upgrade failures.
		s.readers = db
	} else {
		readDSN := (&url.URL{Scheme: "file", Path: absPath}).String() + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(10)&_pragma=query_only(1)"
		s.readers, e = sql.Open("sqlite", readDSN)
		if e != nil {
			db.Close()
			return nil, e
		}
		s.readers.SetMaxOpenConns(sqliteReaderLimit)
		s.readers.SetMaxIdleConns(sqliteReaderLimit)
	}
	return s, nil
}
func (s *SQLiteStore) Close() error {
	if s.readers != s.db {
		return errors.Join(s.readers.Close(), s.db.Close())
	}
	return s.db.Close()
}
func (s *SQLiteStore) View(ctx context.Context, fn func(ReadStore) error) error {
	tx, e := s.begin(ctx, &sql.TxOptions{ReadOnly: true})
	if e != nil {
		return e
	}
	defer tx.Rollback()
	v := &sqlStore{q: transactionQuery{tx, ctx}}
	if e = fn(v); e != nil {
		return e
	}
	return tx.Commit()
}
func (s *SQLiteStore) Update(ctx context.Context, fn func(WriteStore) error) error {
	select {
	case s.writers <- struct{}{}:
		defer func() { <-s.writers }()
	case <-ctx.Done():
		return ctx.Err()
	}
	tx, e := s.begin(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	v := &sqlStore{q: transactionQuery{tx, ctx}}
	if e = fn(v); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *SQLiteStore) begin(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	deadline := time.Now().Add(15 * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		db := s.db
		if opts != nil && opts.ReadOnly {
			db = s.readers
		}
		tx, err := db.BeginTx(ctx, opts)
		if err == nil {
			if err := ctx.Err(); err != nil {
				tx.Rollback()
				return nil, err
			}
			return tx, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var sqliteErr *sqlite.Error
		if !errors.As(err, &sqliteErr) || (sqliteErr.Code()&0xff != sqlite3.SQLITE_BUSY && sqliteErr.Code()&0xff != sqlite3.SQLITE_LOCKED) || !time.Now().Before(deadline) {
			return nil, err
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Store methods share the transaction's cancellation and snapshot.
type transactionQuery struct {
	tx  *sql.Tx
	ctx context.Context
}

func (q transactionQuery) Exec(query string, args ...any) (sql.Result, error) {
	return q.tx.ExecContext(q.ctx, query, args...)
}
func (q transactionQuery) Query(query string, args ...any) (*sql.Rows, error) {
	return q.tx.QueryContext(q.ctx, query, args...)
}
func (q transactionQuery) QueryRow(query string, args ...any) *sql.Row {
	return q.tx.QueryRowContext(q.ctx, query, args...)
}

type querier interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
}
type sqlStore struct {
	q              querier
	grantEvaluator *GrantEvaluator
}

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
