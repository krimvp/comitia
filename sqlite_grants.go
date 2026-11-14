package comitia

import (
	"database/sql"
	"time"
)

const grantSchema = `
CREATE TABLE IF NOT EXISTS grants (id TEXT PRIMARY KEY, session_id TEXT NOT NULL REFERENCES sessions(id), context_id TEXT NOT NULL REFERENCES contexts(id), parent_grant_id TEXT REFERENCES grants(id), issuer_session_id TEXT NOT NULL REFERENCES sessions(id), permissions INTEGER NOT NULL, expires_at TEXT NOT NULL, revoked_at TEXT, created_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS grants_lookup ON grants(session_id,context_id);
CREATE INDEX IF NOT EXISTS grants_context_sessions ON grants(context_id,session_id);
CREATE INDEX IF NOT EXISTS grants_parent ON grants(parent_grant_id);
`

// Revocation cascades seek grants_parent; see revokeSessionTreeSQL.
const revokeGrantTreeSQL = `WITH RECURSIVE d(id) AS (SELECT id FROM grants WHERE id=? UNION ALL SELECT g.id FROM grants g JOIN d ON g.parent_grant_id=d.id) UPDATE grants SET revoked_at=? WHERE id IN (SELECT id FROM d) AND revoked_at IS NULL`

func (x *sqlStore) PutGrant(g Grant) error {
	x.invalidateGrantPolicy()
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
	candidates, err := x.rawGrants("SELECT id,session_id,context_id,parent_grant_id,issuer_session_id,permissions,expires_at,revoked_at,created_at FROM grants WHERE session_id=? AND context_id=?", sid, cid)
	if err != nil {
		return nil, err
	}
	return x.effectiveGrants(candidates, now)
}

func (x *sqlStore) rawGrants(query string, args ...any) ([]Grant, error) {
	rows, err := x.q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var grants []Grant
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

func (x *sqlStore) effectiveGrants(candidates []Grant, now time.Time) ([]Grant, error) {
	policy := x.grantPolicy()
	for _, g := range candidates {
		rememberGrant(policy, g)
	}
	var out []Grant
	for _, g := range candidates {
		ok, err := policy.Effective(g, now)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, g)
		}
	}
	return out, nil
}

func (x *sqlStore) RevokeGrantTree(root string, at time.Time) error {
	x.invalidateGrantPolicy()
	r, e := x.q.Exec(revokeGrantTreeSQL, root, ts(at))
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		var one int
		return dbErr(x.q.QueryRow("SELECT 1 FROM grants WHERE id=?", root).Scan(&one))
	}
	return nil
}
