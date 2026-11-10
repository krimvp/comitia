package comitia

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
)

// ChangeVersionStore is an optional Store capability that lets idle message
// waits skip unchanged polls. ChangeVersion runs outside any transaction. Its
// result must differ from every earlier result of the same store once any
// commit by this handle, another handle or another process has become visible
// to View; spurious changes are allowed. Errors make waits scan as before.
// Stores without this capability scan on every poll.
type ChangeVersionStore interface {
	ChangeVersion(context.Context) (uint64, error)
}

// sqliteChanges reads PRAGMA data_version on one held connection of its own
// pool, which changes after commits by any other connection to the file. A
// replaced connection bumps the version so old values are never repeated.
type sqliteChanges struct {
	db      *sql.DB // nil for shared-cache memory stores
	mu      sync.Mutex
	conn    *sql.Conn
	raw     int64
	version uint64
	closed  bool
	commits atomic.Uint64 // Memory stores have no connections outside this handle.
}

func (s *SQLiteStore) ChangeVersion(ctx context.Context) (uint64, error) {
	c := &s.changes
	if c.db == nil {
		return c.commits.Load(), nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, errors.New("sqlite store closed")
	}
	if c.conn == nil {
		conn, err := c.db.Conn(ctx)
		if err != nil {
			return 0, err
		}
		c.conn, c.raw = conn, -1
	}
	var raw int64
	if err := c.conn.QueryRowContext(ctx, "PRAGMA data_version").Scan(&raw); err != nil {
		c.conn.Close()
		c.conn = nil
		c.version++
		return 0, err
	}
	if raw != c.raw {
		c.raw = raw
		c.version++
	}
	return c.version, nil
}

func (c *sqliteChanges) close() error {
	if c.db == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
	return c.db.Close()
}
