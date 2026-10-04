package comitia

import "database/sql"

const taskSchema = `
CREATE TABLE IF NOT EXISTS tasks (id TEXT PRIMARY KEY, context_id TEXT NOT NULL REFERENCES contexts(id), title TEXT NOT NULL, body TEXT NOT NULL, status TEXT NOT NULL, lease_owner_session_id TEXT, lease_version INTEGER NOT NULL, lease_expires_at TEXT, lease_token_hash TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS tasks_lookup ON tasks(context_id,status,created_at);
`

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
