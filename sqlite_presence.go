package comitia

import (
	"database/sql"
	"encoding/json"
	"time"
)

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
	if err != nil {
		return err
	}
	// Evict only the writer's own least recently seen rows beyond the cap.
	if _, err = x.q.Exec(`DELETE FROM instance_presence WHERE session_id=? AND context_id=? AND instance_id IN (
 SELECT instance_id FROM instance_presence WHERE session_id=? AND context_id=? AND instance_id<>?
 ORDER BY julianday(last_seen) DESC, instance_id DESC LIMIT -1 OFFSET ?)`,
		p.SessionID, p.ContextID, p.SessionID, p.ContextID, p.ID, MaxInstancesPerSession-1); err != nil {
		return err
	}
	// Prune this context's rows of sessions that ended before the retention window.
	cutoff := ts(p.LastSeen.Add(-InstanceRetention))
	_, err = x.q.Exec(`DELETE FROM instance_presence WHERE context_id=? AND session_id IN (
 SELECT s.id FROM sessions s WHERE s.id IN (SELECT session_id FROM instance_presence WHERE context_id=?)
 AND (julianday(s.expires_at)<=julianday(?) OR julianday(s.revoked_at)<=julianday(?)))`, p.ContextID, p.ContextID, cutoff, cutoff)
	return err
}

func (x *sqlStore) InstanceLastSeen(sid, cid, instance string) (time.Time, error) {
	var seen string
	err := x.q.QueryRow("SELECT last_seen FROM instance_presence WHERE session_id=? AND context_id=? AND instance_id=?", sid, cid, instance).Scan(&seen)
	return parse(seen), dbErr(err)
}

func (x *sqlStore) InstanceLabel(sid, cid, instance string) (string, error) {
	var label string
	err := x.q.QueryRow("SELECT label FROM instance_presence WHERE session_id=? AND context_id=? AND instance_id=?", sid, cid, instance).Scan(&label)
	return label, dbErr(err)
}

func (x *sqlStore) TouchInstancePresence(p InstancePresence) error {
	if _, err := x.q.Exec("SAVEPOINT presence_heartbeat"); err != nil {
		return err
	}
	err := x.PutInstancePresence(p)
	if err != nil {
		_, _ = x.q.Exec("ROLLBACK TO presence_heartbeat")
	}
	_, releaseErr := x.q.Exec("RELEASE presence_heartbeat")
	if err != nil {
		return err
	}
	return releaseErr
}

func (x *sqlStore) Instances(cid string, now time.Time) ([]InstancePresence, error) {
	return x.InstancesAfter(cid, now, "", "", DefaultInstanceListLimit)
}

// Rows of sessions that ended before the retention window are hidden.
func (x *sqlStore) InstancesAfter(cid string, now time.Time, afterSession, afterInstance string, limit int) ([]InstancePresence, error) {
	cutoff := ts(now.Add(-InstanceRetention))
	rows, err := x.q.Query(`SELECT p.session_id,p.context_id,p.instance_id,p.label,p.last_seen,COALESCE(p.resume,''),s.expires_at,s.revoked_at
 FROM instance_presence p JOIN sessions s ON s.id=p.session_id
 WHERE p.context_id=? AND (p.session_id,p.instance_id)>(?,?)
 AND julianday(s.expires_at)>julianday(?) AND (s.revoked_at IS NULL OR julianday(s.revoked_at)>julianday(?))
 ORDER BY p.session_id,p.instance_id LIMIT ?`, cid, afterSession, afterInstance, cutoff, cutoff, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []InstancePresence{}
	for rows.Next() {
		var p InstancePresence
		var seen, resume, expires string
		var revoked sql.NullString
		if err := rows.Scan(&p.SessionID, &p.ContextID, &p.ID, &p.Label, &seen, &resume, &expires, &revoked); err != nil {
			return nil, err
		}
		p.LastSeen = parse(seen)
		p.Active = !revoked.Valid && parse(expires).After(now)
		if resume != "" {
			p.Resume = json.RawMessage(resume)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
