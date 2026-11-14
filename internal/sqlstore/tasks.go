package sqlstore

import (
	"database/sql"
	"encoding/json"

	"github.com/krimvp/comitia/internal/model"
)

// Pages seek the sortable expression indexes; older binaries still use
// tasks_page and tasks_status_page.
var sortableCreated = sortableTime("created_at")

var taskSchema = `
CREATE TABLE IF NOT EXISTS task_mutation_receipts (session_id TEXT NOT NULL REFERENCES sessions(id), operation_id TEXT NOT NULL, fingerprint TEXT NOT NULL, result TEXT NOT NULL, PRIMARY KEY(session_id,operation_id));
CREATE TABLE IF NOT EXISTS tasks (id TEXT PRIMARY KEY, context_id TEXT NOT NULL REFERENCES contexts(id), title TEXT NOT NULL, body TEXT NOT NULL, status TEXT NOT NULL, lease_owner_session_id TEXT, lease_version INTEGER NOT NULL, lease_expires_at TEXT, lease_token_hash TEXT NOT NULL, created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS tasks_lookup ON tasks(context_id,status,created_at);
CREATE INDEX IF NOT EXISTS tasks_page ON tasks(context_id,created_at,id);
CREATE INDEX IF NOT EXISTS tasks_status_page ON tasks(context_id,status,created_at,id);
CREATE INDEX IF NOT EXISTS tasks_created_page ON tasks(context_id,` + sortableCreated + `,id);
CREATE INDEX IF NOT EXISTS tasks_status_created_page ON tasks(context_id,status,` + sortableCreated + `,id);
CREATE TABLE IF NOT EXISTS task_outcomes (task_id TEXT PRIMARY KEY REFERENCES tasks(id), reason TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS task_instances (task_id TEXT PRIMARY KEY REFERENCES tasks(id), instance_id TEXT NOT NULL, label TEXT NOT NULL);
`

func (x *sqlStore) PutTask(t model.Task) error {
	_, e := x.q.Exec(`INSERT INTO tasks VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,lease_owner_session_id=excluded.lease_owner_session_id,lease_version=excluded.lease_version,lease_expires_at=excluded.lease_expires_at,lease_token_hash=excluded.lease_token_hash,updated_at=excluded.updated_at`, t.ID, t.ContextID, t.Title, t.Body, t.Status, nullString(t.LeaseOwnerSessionID), t.LeaseVersion, timeString(t.LeaseExpiresAt), t.LeaseTokenHash, model.TS(t.CreatedAt), model.TS(t.UpdatedAt))
	if e != nil {
		return e
	}
	if t.OutcomeReason != "" {
		_, e = x.q.Exec("INSERT INTO task_outcomes VALUES(?,?) ON CONFLICT(task_id) DO UPDATE SET reason=excluded.reason", t.ID, t.OutcomeReason)
	} else {
		_, e = x.q.Exec("DELETE FROM task_outcomes WHERE task_id=?", t.ID)
	}
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

func scanTask(row interface{ Scan(...any) error }) (model.Task, error) {
	var t model.Task
	var owner, exp sql.NullString
	var c, u, instance, label string
	e := row.Scan(&t.ID, &t.ContextID, &t.Title, &t.Body, &t.Status, &owner, &t.LeaseVersion, &exp, &t.LeaseTokenHash, &c, &u, &instance, &label, &t.OutcomeReason)
	t.LeaseOwnerSessionID = owner.String
	if instance != "" {
		t.LeaseOwnerInstance = &model.AgentInstance{ID: instance, Label: label}
	}
	t.LeaseExpiresAt = nullTime(exp)
	t.CreatedAt = model.ParseTime(c)
	t.UpdatedAt = model.ParseTime(u)
	return t, dbErr(e)
}

const taskSelect = "SELECT t.id,t.context_id,t.title,t.body,t.status,t.lease_owner_session_id,t.lease_version,t.lease_expires_at,t.lease_token_hash,t.created_at,t.updated_at,COALESCE(i.instance_id,''),COALESCE(i.label,''),COALESCE(o.reason,'') FROM tasks t LEFT JOIN task_instances i ON i.task_id=t.id LEFT JOIN task_outcomes o ON o.task_id=t.id"

func (x *sqlStore) Task(id string) (model.Task, error) {
	return scanTask(x.q.QueryRow(taskSelect+" WHERE t.id=?", id))
}

func (x *sqlStore) Tasks(cid string, status model.TaskStatus, limit int) ([]model.Task, error) {
	return x.TasksAfter(cid, status, "", "", limit)
}

func (x *sqlStore) TasksAfter(cid string, status model.TaskStatus, created, afterID string, limit int) ([]model.Task, error) {
	return x.TasksFilteredAfter(cid, status, model.TaskFilters{}, created, afterID, limit)
}

func (x *sqlStore) TasksFilteredAfter(cid string, status model.TaskStatus, f model.TaskFilters, created, afterID string, limit int) ([]model.Task, error) {
	q, args := taskPageQuery(cid, status, f, created, afterID, limit)
	rows, e := x.q.Query(q, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []model.Task
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

// taskPageQuery seeks tasks_created_page or tasks_status_created_page.
func taskPageQuery(cid string, status model.TaskStatus, f model.TaskFilters, created, afterID string, limit int) (string, []any) {
	q := taskSelect + " WHERE t.context_id=?"
	args := []any{cid}
	if status != "" {
		q += " AND t.status=?"
		args = append(args, status)
	}
	if f.Claimable {
		q += " AND t.status IN ('pending','leased')"
	}
	if f.OwnerSession != "" || f.OwnerInstance != "" {
		q += " AND t.status='leased'"
	}
	if f.OwnerSession != "" {
		q += " AND t.lease_owner_session_id=?"
		args = append(args, f.OwnerSession)
	}
	if f.OwnerInstance != "" {
		q += " AND i.instance_id=?"
		args = append(args, f.OwnerInstance)
	}
	if afterID != "" {
		// Expression indexes serve this range, not a row-value comparison.
		q += " AND " + sortableCreated + ">=? AND (" + sortableCreated + ">? OR t.id>?)"
		at := model.SortableTS(model.ParseTime(created))
		args = append(args, at, at, afterID)
	}
	q += " ORDER BY " + sortableCreated + ",t.id LIMIT ?"
	return q, append(args, limit)
}

func (x *sqlStore) TaskReceipt(sid, operationID string) (model.TaskReceipt, error) {
	var receipt model.TaskReceipt
	var raw string
	e := x.q.QueryRow("SELECT fingerprint,result FROM task_mutation_receipts WHERE session_id=? AND operation_id=?", sid, operationID).Scan(&receipt.Fingerprint, &raw)
	if e != nil {
		return receipt, dbErr(e)
	}
	e = json.Unmarshal([]byte(raw), &receipt.Result)
	return receipt, e
}

func (x *sqlStore) PutTaskReceipt(sid, operationID string, receipt model.TaskReceipt) error {
	raw, e := json.Marshal(receipt.Result)
	if e != nil {
		return e
	}
	_, e = x.q.Exec("INSERT INTO task_mutation_receipts VALUES(?,?,?,?)", sid, operationID, receipt.Fingerprint, string(raw))
	return e
}
