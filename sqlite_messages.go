package comitia

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

const messageSchema = `
CREATE TABLE IF NOT EXISTS messages (id TEXT PRIMARY KEY, context_id TEXT NOT NULL REFERENCES contexts(id), session_id TEXT NOT NULL REFERENCES sessions(id), seq INTEGER NOT NULL, dedupe_key TEXT NOT NULL, body TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(context_id,session_id,dedupe_key), UNIQUE(context_id,seq));
CREATE TABLE IF NOT EXISTS message_envelopes (message_id TEXT PRIMARY KEY REFERENCES messages(id), envelope TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS message_instances (message_id TEXT PRIMARY KEY REFERENCES messages(id), instance_id TEXT NOT NULL, label TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS instance_acks (session_id TEXT NOT NULL REFERENCES sessions(id), context_id TEXT NOT NULL REFERENCES contexts(id), instance_id TEXT NOT NULL, seq INTEGER NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(session_id,context_id,instance_id));
CREATE TABLE IF NOT EXISTS message_dedupe_scopes (context_id TEXT NOT NULL REFERENCES contexts(id), session_id TEXT NOT NULL REFERENCES sessions(id), instance_id TEXT NOT NULL, dedupe_key TEXT NOT NULL, message_id TEXT NOT NULL UNIQUE REFERENCES messages(id), PRIMARY KEY(context_id,session_id,instance_id,dedupe_key));
INSERT INTO message_dedupe_scopes SELECT m.context_id,m.session_id,COALESCE(i.instance_id,''),m.dedupe_key,m.id FROM messages m LEFT JOIN message_instances i ON i.message_id=m.id WHERE NOT EXISTS (SELECT 1 FROM message_dedupe_scopes d WHERE d.message_id=m.id);
CREATE TABLE IF NOT EXISTS acks (session_id TEXT NOT NULL REFERENCES sessions(id), context_id TEXT NOT NULL REFERENCES contexts(id), seq INTEGER NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(session_id,context_id));
`

func (x *sqlStore) MessageByDedupe(cid, sid, key string, instances ...string) (Message, error) {
	instance, err := cursorInstance(instances)
	if err != nil {
		return Message{}, err
	}
	return scanMessage(x.q.QueryRow(messageSelect+" WHERE d.context_id=? AND d.session_id=? AND d.instance_id=? AND d.dedupe_key=?", cid, sid, instance, key))
}

const messageSelect = "SELECT m.id,m.context_id,m.session_id,m.seq,COALESCE(d.dedupe_key,m.dedupe_key),m.body,m.created_at,COALESCE(e.envelope,''),COALESCE(i.instance_id,''),COALESCE(i.label,'') FROM messages m LEFT JOIN message_envelopes e ON e.message_id=m.id LEFT JOIN message_instances i ON i.message_id=m.id LEFT JOIN message_dedupe_scopes d ON d.message_id=m.id"

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
	// Keep natural keys readable by older binaries when the legacy tuple is
	// free. Only instance collisions need an opaque, collision-checked key.
	storageKey := m.DedupeKey
	for attempt := 0; ; attempt++ {
		var exists bool
		if e = x.q.QueryRow("SELECT EXISTS(SELECT 1 FROM messages WHERE context_id=? AND session_id=? AND dedupe_key=?)", m.ContextID, m.SessionID, storageKey).Scan(&exists); e != nil {
			return m, e
		}
		if !exists {
			break
		}
		if attempt == 0 {
			storageKey = m.ID
		} else {
			storageKey += "."
		}
	}
	_, e = x.q.Exec("INSERT INTO messages VALUES(?,?,?,?,?,?,?)", m.ID, m.ContextID, m.SessionID, m.Sequence, storageKey, m.Body, ts(m.CreatedAt))
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
	if e == nil {
		instance := ""
		if m.AgentInstance != nil {
			instance = m.AgentInstance.ID
		}
		_, e = x.q.Exec("INSERT INTO message_dedupe_scopes VALUES(?,?,?,?,?)", m.ContextID, m.SessionID, instance, m.DedupeKey, m.ID)
	}
	return m, e
}
func (x *sqlStore) Messages(cid string, after int64, limit int, filters ...MessageFilters) ([]Message, error) {
	f, err := messageFilters(filters)
	if err != nil {
		return nil, err
	}
	query := messageSelect + " WHERE m.context_id=? AND m.seq>?"
	args := []any{cid, after}
	if f.ForInstance != "" {
		query += ` AND ((COALESCE(json_array_length(e.envelope,'$.recipient_instances'),0)=0 AND COALESCE(json_array_length(e.envelope,'$.recipient_session_ids'),0)=0) OR EXISTS (SELECT 1 FROM json_each(e.envelope,'$.recipient_instances') WHERE value=?))`
		args = append(args, f.ForInstance)
	}
	if f.FromInstance != "" {
		query += " AND i.instance_id=?"
		args = append(args, f.FromInstance)
	}
	if f.TaskID != "" {
		query += " AND json_extract(e.envelope,'$.task_id')=?"
		args = append(args, f.TaskID)
	}
	if f.Schema != "" {
		query += " AND json_extract(e.envelope,'$.schema')=?"
		args = append(args, f.Schema)
	}
	args = append(args, limit)
	rows, e := x.q.Query(query+" ORDER BY m.seq LIMIT ?", args...)
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

func (x *sqlStore) Participants(cid string, now time.Time, limit int) ([]Participant, error) {
	rows, err := x.q.Query("SELECT DISTINCT session_id FROM grants WHERE context_id=? ORDER BY session_id", cid)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var sid string
		if err = rows.Scan(&sid); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, sid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	out := []Participant{}
	for _, sid := range ids {
		session, err := x.Session(sid)
		if err != nil {
			return nil, err
		}
		if session.RevokedAt != nil || !session.ExpiresAt.After(now) {
			continue
		}
		grants, err := x.Grants(sid, cid, now)
		if err != nil {
			return nil, err
		}
		readable := false
		for _, g := range grants {
			if g.Permissions&PermissionRead != 0 {
				readable = true
				break
			}
		}
		if !readable {
			continue
		}
		principal, err := x.Principal(session.PrincipalID)
		if err != nil {
			return nil, err
		}
		out = append(out, Participant{SessionID: sid, PrincipalID: principal.ID, Name: principal.Name, ExpiresAt: session.ExpiresAt})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}
