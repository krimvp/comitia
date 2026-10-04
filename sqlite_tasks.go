package comitia

import "database/sql"

const taskSchema = `
CREATE TABLE IF NOT EXISTS tasks (id TEXT PRIMARY KEY, context_id TEXT NOT NULL REFERENCES contexts(id), title TEXT NOT NULL, body TEXT NOT NULL, status TEXT NOT NULL, lease_owner_session_id TEXT, lease_version INTEGER NOT NULL, lease_expires_at TEXT, lease_token_hash TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS tasks_lookup ON tasks(context_id,status,created_at);
CREATE INDEX IF NOT EXISTS tasks_page ON tasks(context_id,created_at,id);
CREATE INDEX IF NOT EXISTS tasks_status_page ON tasks(context_id,status,created_at,id);
CREATE TABLE IF NOT EXISTS task_instances (task_id TEXT PRIMARY KEY REFERENCES tasks(id), instance_id TEXT NOT NULL, label TEXT NOT NULL);
`

func (x *sqlStore) PutTask(t Task) error {
	_, e := x.q.Exec(`INSERT INTO tasks VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,lease_owner_session_id=excluded.lease_owner_session_id,lease_version=excluded.lease_version,lease_expires_at=excluded.lease_expires_at,lease_token_hash=excluded.lease_token_hash,updated_at=excluded.updated_at`, t.ID, t.ContextID, t.Title, t.Body, t.Status, nullString(t.LeaseOwnerSessionID), t.LeaseVersion, timeString(t.LeaseExpiresAt), t.LeaseTokenHash, ts(t.CreatedAt), ts(t.UpdatedAt))
	if e != nil {
		return e
	}
	if t.LeaseOwnerInstance == nil {
		_, e = x.q.Exec("DELETE FROM task_instances WHERE task_id=?", t.ID)
		return e
	}
	_, e = x.q.Exec("INSERT INTO task_instances VALUES(?,?,?) ON CONFLICT(task_id) DO UPDATE SET instance_id=excluded.instance_id,label=excluded.label", t.ID, t.LeaseOwnerInstance.ID, t.LeaseOwnerInstance.Label)
	return e
}
func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	var owner, exp sql.NullString
	var c, u, instance, label string
	e := row.Scan(&t.ID, &t.ContextID, &t.Title, &t.Body, &t.Status, &owner, &t.LeaseVersion, &exp, &t.LeaseTokenHash, &c, &u, &instance, &label)
	t.LeaseOwnerSessionID = owner.String
	if instance != "" {
		t.LeaseOwnerInstance = &AgentInstance{ID: instance, Label: label}
	}
	t.LeaseExpiresAt = nullTime(exp)
	t.CreatedAt = parse(c)
	t.UpdatedAt = parse(u)
	return t, dbErr(e)
}

const taskSelect = "SELECT t.id,t.context_id,t.title,t.body,t.status,t.lease_owner_session_id,t.lease_version,t.lease_expires_at,t.lease_token_hash,t.created_at,t.updated_at,COALESCE(i.instance_id,''),COALESCE(i.label,'') FROM tasks t LEFT JOIN task_instances i ON i.task_id=t.id"

func (x *sqlStore) Task(id string) (Task, error) {
	return scanTask(x.q.QueryRow(taskSelect+" WHERE t.id=?", id))
}
func (x *sqlStore) Tasks(cid string, status TaskStatus, limit int) ([]Task, error) {
	return x.TasksAfter(cid, status, "", "", limit)
}
func (x *sqlStore) TasksAfter(cid string, status TaskStatus, created, afterID string, limit int) ([]Task, error) {
	q := taskSelect + " WHERE t.context_id=?"
	args := []any{cid}
	if status != "" {
		q += " AND t.status=?"
		args = append(args, status)
	}
	if afterID != "" {
		q += " AND (t.created_at,t.id)>(?,?)"
		args = append(args, created, afterID)
	}
	q += " ORDER BY t.created_at,t.id LIMIT ?"
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
