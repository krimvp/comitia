package comitia

const contextSchema = `
CREATE TABLE IF NOT EXISTS workspaces (id TEXT PRIMARY KEY, name TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS contexts (id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL REFERENCES workspaces(id), name TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS refs (id TEXT PRIMARY KEY, context_id TEXT NOT NULL REFERENCES contexts(id), kind TEXT NOT NULL, uri TEXT NOT NULL, created_at TEXT NOT NULL);
`

// contextTreeSchema keeps child-context metadata in a side table, so existing contexts simply have no row (no parent).
const contextTreeSchema = `
CREATE TABLE IF NOT EXISTS context_parents (context_id TEXT PRIMARY KEY REFERENCES contexts(id), parent_context_id TEXT NOT NULL REFERENCES contexts(id), task_id TEXT REFERENCES tasks(id), creator_session_id TEXT NOT NULL REFERENCES sessions(id), parent_readable INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS context_parents_lookup ON context_parents(parent_context_id,context_id);
`

const contextColumns = `c.id,c.workspace_id,c.name,c.created_at,COALESCE(p.parent_context_id,''),COALESCE(p.task_id,''),COALESCE(p.creator_session_id,''),COALESCE(p.parent_readable,0)`

func scanContext(row interface{ Scan(...any) error }) (WorkContext, error) {
	var c WorkContext
	var at string
	e := row.Scan(&c.ID, &c.WorkspaceID, &c.Name, &at, &c.ParentContextID, &c.TaskID, &c.CreatorSessionID, &c.ParentReadable)
	c.CreatedAt = parse(at)
	return c, dbErr(e)
}
func (x *sqlStore) contexts(query string, args ...any) ([]WorkContext, error) {
	rows, err := x.q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WorkContext{}
	for rows.Next() {
		c, err := scanContext(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
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
	if _, e := x.q.Exec("INSERT INTO contexts VALUES(?,?,?,?)", c.ID, c.WorkspaceID, c.Name, ts(c.CreatedAt)); e != nil || c.ParentContextID == "" {
		return e
	}
	_, e := x.q.Exec("INSERT INTO context_parents VALUES(?,?,?,?,?)", c.ID, c.ParentContextID, nullString(c.TaskID), c.CreatorSessionID, c.ParentReadable)
	return e
}
func (x *sqlStore) Context(id string) (WorkContext, error) {
	return scanContext(x.q.QueryRow("SELECT "+contextColumns+" FROM contexts c LEFT JOIN context_parents p ON p.context_id=c.id WHERE c.id=?", id))
}
func (x *sqlStore) PutReference(r Reference) error {
	_, e := x.q.Exec("INSERT INTO refs VALUES(?,?,?,?,?)", r.ID, r.ContextID, r.Kind, r.URI, ts(r.CreatedAt))
	return e
}

// ContextCandidates only narrows discovery; Service rechecks effective read
// grants, including ancestry expiry and revocation, before returning any row.
func (x *sqlStore) ContextCandidates(sessionID, afterID string, limit int) ([]WorkContext, error) {
	return x.contexts(`SELECT DISTINCT `+contextColumns+`
FROM contexts c JOIN grants g ON g.context_id=c.id LEFT JOIN context_parents p ON p.context_id=c.id
WHERE g.session_id=? AND c.id>? ORDER BY c.id LIMIT ?`, sessionID, afterID, limit)
}
func (x *sqlStore) ChildContexts(parentID, afterID string, limit int) ([]WorkContext, error) {
	return x.contexts(`SELECT `+contextColumns+`
FROM context_parents p JOIN contexts c ON c.id=p.context_id
WHERE p.parent_context_id=? AND c.id>? ORDER BY c.id LIMIT ?`, parentID, afterID, limit)
}
