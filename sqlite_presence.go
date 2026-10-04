package comitia

import "encoding/json"

const presenceSchema = `
CREATE TABLE IF NOT EXISTS instance_presence (
 session_id TEXT NOT NULL REFERENCES sessions(id),
 context_id TEXT NOT NULL REFERENCES contexts(id),
 instance_id TEXT NOT NULL,
 label TEXT NOT NULL,
 last_seen TEXT NOT NULL,
 resume TEXT,
 PRIMARY KEY(session_id,context_id,instance_id)
);
CREATE INDEX IF NOT EXISTS instance_presence_context ON instance_presence(context_id);
`

func (x *sqlStore) PutInstancePresence(p InstancePresence) error {
	var resume any
	if p.Resume != nil {
		resume = string(p.Resume)
	}
	_, err := x.q.Exec(`INSERT INTO instance_presence(session_id,context_id,instance_id,label,last_seen,resume) VALUES(?,?,?,?,?,?)
 ON CONFLICT(session_id,context_id,instance_id) DO UPDATE SET
 label=CASE WHEN excluded.label='' THEN instance_presence.label ELSE excluded.label END,
 last_seen=excluded.last_seen, resume=COALESCE(excluded.resume,instance_presence.resume)`,
		p.SessionID, p.ContextID, p.ID, p.Label, ts(p.LastSeen), resume)
	return err
}

func (x *sqlStore) Instances(cid string) ([]InstancePresence, error) {
	rows, err := x.q.Query("SELECT session_id,context_id,instance_id,label,last_seen,COALESCE(resume,'') FROM instance_presence WHERE context_id=? ORDER BY session_id,instance_id", cid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InstancePresence{}
	for rows.Next() {
		var p InstancePresence
		var seen, resume string
		if err := rows.Scan(&p.SessionID, &p.ContextID, &p.ID, &p.Label, &seen, &resume); err != nil {
			return nil, err
		}
		p.LastSeen = parse(seen)
		if resume != "" {
			p.Resume = json.RawMessage(resume)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
