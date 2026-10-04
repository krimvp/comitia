package comitia

import (
	"database/sql"
	"time"
)

const grantSchema = `
CREATE TABLE IF NOT EXISTS grants (id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id), context_id TEXT NOT NULL REFERENCES contexts(id), parent_grant_id TEXT REFERENCES grants(id), issuer_session_id TEXT NOT NULL REFERENCES sessions(id), permissions INTEGER NOT NULL, expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS grants_lookup ON grants(session_id,context_id);
`

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
func (x *sqlStore) Grant(id string) (Grant, error) { return x.grant(id) }
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
