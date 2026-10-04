package comitia

import (
	"context"
	"database/sql"
	"encoding/json"
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
	_, e := s.db.ExecContext(ctx, schema)
	return e
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

const schema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS principals (id TEXT PRIMARY KEY, name TEXT NOT NULL, admin INTEGER NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, principal_id TEXT NOT NULL REFERENCES principals(id), parent_id TEXT REFERENCES sessions(id), token_hash TEXT NOT NULL UNIQUE, expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS workspaces (id TEXT PRIMARY KEY, name TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS contexts (id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL REFERENCES workspaces(id), name TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS refs (id TEXT PRIMARY KEY, context_id TEXT NOT NULL REFERENCES contexts(id), kind TEXT NOT NULL, uri TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS grants (id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id), context_id TEXT NOT NULL REFERENCES contexts(id), parent_grant_id TEXT REFERENCES grants(id), issuer_session_id TEXT NOT NULL REFERENCES sessions(id), permissions INTEGER NOT NULL, expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS grants_lookup ON grants(session_id,context_id);
CREATE TABLE IF NOT EXISTS messages (id TEXT PRIMARY KEY, context_id TEXT NOT NULL REFERENCES contexts(id), session_id TEXT NOT NULL REFERENCES sessions(id), seq INTEGER NOT NULL, dedupe_key TEXT NOT NULL, body TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(context_id,session_id,dedupe_key), UNIQUE(context_id,seq));
CREATE TABLE IF NOT EXISTS message_envelopes (message_id TEXT PRIMARY KEY REFERENCES messages(id), envelope TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS message_instances (message_id TEXT PRIMARY KEY REFERENCES messages(id), instance_id TEXT NOT NULL, label TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS instance_acks (session_id TEXT NOT NULL REFERENCES sessions(id), context_id TEXT NOT NULL REFERENCES contexts(id), instance_id TEXT NOT NULL, seq INTEGER NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(session_id,context_id,instance_id));
CREATE TABLE IF NOT EXISTS acks (session_id TEXT NOT NULL REFERENCES sessions(id), context_id TEXT NOT NULL REFERENCES contexts(id), seq INTEGER NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(session_id,context_id));
CREATE TABLE IF NOT EXISTS tasks (id TEXT PRIMARY KEY, context_id TEXT NOT NULL REFERENCES contexts(id), title TEXT NOT NULL, body TEXT NOT NULL, status TEXT NOT NULL, lease_owner_session_id TEXT, lease_version INTEGER NOT NULL, lease_expires_at TEXT, lease_token_hash TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS tasks_lookup ON tasks(context_id,status,created_at);
`

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
func (x *sqlStore) BootstrapDone() (bool, error) {
	var v string
	e := x.q.QueryRow("SELECT value FROM meta WHERE key='bootstrap'").Scan(&v)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	return v == "done", e
}
func (x *sqlStore) MarkBootstrapDone() error {
	_, e := x.q.Exec("INSERT INTO meta(key,value) VALUES('bootstrap','done')")
	return e
}
func (x *sqlStore) PutPrincipal(p Principal) error {
	_, e := x.q.Exec("INSERT INTO principals VALUES(?,?,?,?)", p.ID, p.Name, p.Admin, ts(p.CreatedAt))
	return e
}
func (x *sqlStore) Principal(id string) (Principal, error) {
	var p Principal
	var a int
	var c string
	e := x.q.QueryRow("SELECT id,name,admin,created_at FROM principals WHERE id=?", id).Scan(&p.ID, &p.Name, &a, &c)
	p.Admin = a != 0
	p.CreatedAt = parse(c)
	return p, dbErr(e)
}
func (x *sqlStore) PutSession(s Session, h string) error {
	_, e := x.q.Exec("INSERT INTO sessions VALUES(?,?,?,?,?,?,?)", s.ID, s.PrincipalID, nullString(s.ParentID), h, ts(s.ExpiresAt), timeString(s.RevokedAt), ts(s.CreatedAt))
	return e
}
func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var s Session
	var parent, rev sql.NullString
	var exp, created string
	e := row.Scan(&s.ID, &s.PrincipalID, &parent, &exp, &rev, &created)
	s.ParentID = parent.String
	s.ExpiresAt = parse(exp)
	s.RevokedAt = nullTime(rev)
	s.CreatedAt = parse(created)
	return s, dbErr(e)
}
func (x *sqlStore) Session(id string) (Session, error) {
	return scanSession(x.q.QueryRow("SELECT id,principal_id,parent_id,expires_at,revoked_at,created_at FROM sessions WHERE id=?", id))
}
func (x *sqlStore) SessionByTokenHash(h string) (Session, error) {
	return scanSession(x.q.QueryRow("SELECT id,principal_id,parent_id,expires_at,revoked_at,created_at FROM sessions WHERE token_hash=?", h))
}
func (x *sqlStore) RevokeSessionTree(root string, at time.Time) error {
	r, e := x.q.Exec(`WITH RECURSIVE d(id) AS (SELECT id FROM sessions WHERE id=? UNION ALL SELECT s.id FROM sessions s JOIN d ON s.parent_id=d.id) UPDATE sessions SET revoked_at=? WHERE id IN (SELECT id FROM d) AND revoked_at IS NULL`, root, ts(at))
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (x *sqlStore) PutWorkspace(w Workspace) error {
	_, e := x.q.Exec("INSERT INTO workspaces VALUES(?,?,?)", w.ID, w.Name, ts(w.CreatedAt))
	return e
}
func (x *sqlStore) Workspace(id string) (Workspace, error) {
	var w Workspace
	var c string
	e := x.q.QueryRow("SELECT id,name,created_at FROM workspaces WHERE id=?", id).Scan(&w.ID, &w.Name, &c)
	w.CreatedAt = parse(c)
	return w, dbErr(e)
}
func (x *sqlStore) PutContext(c WorkContext) error {
	_, e := x.q.Exec("INSERT INTO contexts VALUES(?,?,?,?)", c.ID, c.WorkspaceID, c.Name, ts(c.CreatedAt))
	return e
}
func (x *sqlStore) Context(id string) (WorkContext, error) {
	var c WorkContext
	var at string
	e := x.q.QueryRow("SELECT id,workspace_id,name,created_at FROM contexts WHERE id=?", id).Scan(&c.ID, &c.WorkspaceID, &c.Name, &at)
	c.CreatedAt = parse(at)
	return c, dbErr(e)
}
func (x *sqlStore) PutReference(r Reference) error {
	_, e := x.q.Exec("INSERT INTO refs VALUES(?,?,?,?,?)", r.ID, r.ContextID, r.Kind, r.URI, ts(r.CreatedAt))
	return e
}
func (x *sqlStore) PutGrant(g Grant) error {
	_, e := x.q.Exec("INSERT INTO grants VALUES(?,?,?,?,?,?,?,?,?)", g.ID, g.SessionID, g.ContextID, nullString(g.ParentGrantID), g.IssuerSessionID, g.Permissions, ts(g.ExpiresAt), timeString(g.RevokedAt), ts(g.CreatedAt))
	return e
}
func scanGrant(row interface{ Scan(...any) error }) (Grant, error) {
	var g Grant
	var par, rev sql.NullString
	var exp, created string
	e := row.Scan(&g.ID, &g.SessionID, &g.ContextID, &par, &g.IssuerSessionID, &g.Permissions, &exp, &rev, &created)
	g.ParentGrantID = par.String
	g.ExpiresAt = parse(exp)
	g.RevokedAt = nullTime(rev)
	g.CreatedAt = parse(created)
	return g, dbErr(e)
}
func (x *sqlStore) grant(id string) (Grant, error) {
	return scanGrant(x.q.QueryRow("SELECT id,session_id,context_id,parent_grant_id,issuer_session_id,permissions,expires_at,revoked_at,created_at FROM grants WHERE id=?", id))
}
func (x *sqlStore) Grants(sid, cid string, now time.Time) ([]Grant, error) {
	rows, e := x.q.Query("SELECT id,session_id,context_id,parent_grant_id,issuer_session_id,permissions,expires_at,revoked_at,created_at FROM grants WHERE session_id=? AND context_id=?", sid, cid)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var candidates []Grant
	for rows.Next() {
		g, e := scanGrant(rows)
		if e != nil {
			return nil, e
		}
		candidates = append(candidates, g)
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	var out []Grant
	for _, g := range candidates {
		ok := true
		seen := map[string]bool{}
		for cur := g; ; {
			owner, ownerErr := x.Session(cur.SessionID)
			issuer, issuerErr := x.Session(cur.IssuerSessionID)
			if seen[cur.ID] || cur.RevokedAt != nil || !cur.ExpiresAt.After(now) || ownerErr != nil || owner.RevokedAt != nil || !owner.ExpiresAt.After(now) || issuerErr != nil || issuer.RevokedAt != nil || !issuer.ExpiresAt.After(now) {
				ok = false
				break
			}
			seen[cur.ID] = true
			if cur.ParentGrantID == "" {
				break
			}
			cur, e = x.grant(cur.ParentGrantID)
			if e != nil {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, g)
		}
	}
	return out, nil
}
func (x *sqlStore) RevokeGrantTree(root string, at time.Time) error {
	r, e := x.q.Exec(`WITH RECURSIVE d(id) AS (SELECT id FROM grants WHERE id=? UNION ALL SELECT g.id FROM grants g JOIN d ON g.parent_grant_id=d.id) UPDATE grants SET revoked_at=? WHERE id IN (SELECT id FROM d) AND revoked_at IS NULL`, root, ts(at))
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
func (x *sqlStore) MessageByDedupe(cid, sid, key string) (Message, error) {
	return scanMessage(x.q.QueryRow(messageSelect+" WHERE m.context_id=? AND m.session_id=? AND m.dedupe_key=?", cid, sid, key))
}

const messageSelect = "SELECT m.id,m.context_id,m.session_id,m.seq,m.dedupe_key,m.body,m.created_at,COALESCE(e.envelope,''),COALESCE(i.instance_id,''),COALESCE(i.label,'') FROM messages m LEFT JOIN message_envelopes e ON e.message_id=m.id LEFT JOIN message_instances i ON i.message_id=m.id"

func (x *sqlStore) Message(id string) (Message, error) {
	return scanMessage(x.q.QueryRow(messageSelect+" WHERE m.id=?", id))
}
func scanMessage(row interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var c, envelope, instance, label string
	e := row.Scan(&m.ID, &m.ContextID, &m.SessionID, &m.Sequence, &m.DedupeKey, &m.Body, &c, &envelope, &instance, &label)
	if e == nil && envelope != "" {
		e = json.Unmarshal([]byte(envelope), &m.Envelope)
	}
	if instance != "" {
		m.AgentInstance = &AgentInstance{ID: instance, Label: label}
		m.AgentInstanceSelfReported = true
	}
	m.CreatedAt = parse(c)
	return m, dbErr(e)
}
func (x *sqlStore) PutMessage(m Message) (Message, error) {
	e := x.q.QueryRow("SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE context_id=?", m.ContextID).Scan(&m.Sequence)
	if e != nil {
		return m, e
	}
	_, e = x.q.Exec("INSERT INTO messages VALUES(?,?,?,?,?,?,?)", m.ID, m.ContextID, m.SessionID, m.Sequence, m.DedupeKey, m.Body, ts(m.CreatedAt))
	if e == nil && m.Envelope != nil {
		var raw []byte
		raw, e = json.Marshal(m.Envelope)
		if e == nil {
			_, e = x.q.Exec("INSERT INTO message_envelopes VALUES(?,?)", m.ID, string(raw))
		}
	}
	if e == nil && m.AgentInstance != nil {
		_, e = x.q.Exec("INSERT INTO message_instances VALUES(?,?,?)", m.ID, m.AgentInstance.ID, m.AgentInstance.Label)
	}
	return m, e
}
func (x *sqlStore) Messages(cid string, after int64, limit int) ([]Message, error) {
	rows, e := x.q.Query(messageSelect+" WHERE m.context_id=? AND m.seq>? ORDER BY m.seq LIMIT ?", cid, after, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			return nil, e
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (x *sqlStore) Ack(sid, cid string, seq int64, at time.Time, instance string) error {
	var max int64
	if e := x.q.QueryRow("SELECT COALESCE(MAX(seq),0) FROM messages WHERE context_id=?", cid).Scan(&max); e != nil {
		return e
	}
	if seq > max {
		return ErrInvalid
	}
	if instance != "" {
		_, e := x.q.Exec(`INSERT INTO instance_acks VALUES(?,?,?,?,?) ON CONFLICT(session_id,context_id,instance_id) DO UPDATE SET seq=MAX(seq,excluded.seq),updated_at=excluded.updated_at`, sid, cid, instance, seq, ts(at))
		return e
	}
	_, e := x.q.Exec(`INSERT INTO acks VALUES(?,?,?,?) ON CONFLICT(session_id,context_id) DO UPDATE SET seq=MAX(seq,excluded.seq),updated_at=excluded.updated_at`, sid, cid, seq, ts(at))
	return e
}
func (x *sqlStore) AckCursor(sid, cid, instance string) (int64, error) {
	var seq int64
	var e error
	if instance == "" {
		e = x.q.QueryRow("SELECT seq FROM acks WHERE session_id=? AND context_id=?", sid, cid).Scan(&seq)
	} else {
		e = x.q.QueryRow("SELECT seq FROM instance_acks WHERE session_id=? AND context_id=? AND instance_id=?", sid, cid, instance).Scan(&seq)
	}
	if errors.Is(e, sql.ErrNoRows) {
		return 0, nil
	}
	return seq, e
}
func (x *sqlStore) PutTask(t Task) error {
	_, e := x.q.Exec(`INSERT INTO tasks VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,lease_owner_session_id=excluded.lease_owner_session_id,lease_version=excluded.lease_version,lease_expires_at=excluded.lease_expires_at,lease_token_hash=excluded.lease_token_hash,updated_at=excluded.updated_at`, t.ID, t.ContextID, t.Title, t.Body, t.Status, nullString(t.LeaseOwnerSessionID), t.LeaseVersion, timeString(t.LeaseExpiresAt), t.LeaseTokenHash, ts(t.CreatedAt), ts(t.UpdatedAt))
	return e
}
func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var owner, exp sql.NullString
	var c, u string
	e := row.Scan(&t.ID, &t.ContextID, &t.Title, &t.Body, &t.Status, &owner, &t.LeaseVersion, &exp, &t.LeaseTokenHash, &c, &u)
	t.LeaseOwnerSessionID = owner.String
	t.LeaseExpiresAt = nullTime(exp)
	t.CreatedAt = parse(c)
	t.UpdatedAt = parse(u)
	return t, dbErr(e)
}
func (x *sqlStore) Task(id string) (Task, error) {
	return scanTask(x.q.QueryRow("SELECT id,context_id,title,body,status,lease_owner_session_id,lease_version,lease_expires_at,lease_token_hash,created_at,updated_at FROM tasks WHERE id=?", id))
}
func (x *sqlStore) Tasks(cid string, status TaskStatus, limit int) ([]Task, error) {
	q := "SELECT id,context_id,title,body,status,lease_owner_session_id,lease_version,lease_expires_at,lease_token_hash,created_at,updated_at FROM tasks WHERE context_id=?"
	args := []any{cid}
	if status != "" {
		q += " AND status=?"
		args = append(args, status)
	}
	q += " ORDER BY created_at LIMIT ?"
	args = append(args, limit)
	rows, e := x.q.Query(q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, e := scanTask(rows)
		if e != nil {
			return nil, e
		}
		t.LeaseTokenHash = ""
		out = append(out, t)
	}
	return out, rows.Err()
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
