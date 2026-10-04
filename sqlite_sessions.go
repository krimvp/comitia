package comitia

import (
	"database/sql"
	"errors"
	"time"
)

const sessionSchema = `
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS principals (id TEXT PRIMARY KEY, name TEXT NOT NULL, admin INTEGER NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS sessions (id TEXT PRIMARY KEY, principal_id TEXT NOT NULL REFERENCES principals(id), parent_id TEXT REFERENCES sessions(id), token_hash TEXT NOT NULL UNIQUE, expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL);
`

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
	x.invalidateGrantPolicy()
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
	x.invalidateGrantPolicy()
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
