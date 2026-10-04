package comitia

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

const messageSchema = `
CREATE TABLE IF NOT EXISTS message_task_attempts (message_id TEXT PRIMARY KEY REFERENCES messages(id), task_id TEXT NOT NULL, lease_version INTEGER NOT NULL, claim_token_hash TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS messages (id TEXT PRIMARY KEY, context_id TEXT NOT NULL REFERENCES contexts(id), session_id TEXT NOT NULL REFERENCES sessions(id), seq INTEGER NOT NULL, dedupe_key TEXT NOT NULL, body TEXT NOT NULL, created_at TEXT NOT NULL, UNIQUE(context_id,session_id,dedupe_key), UNIQUE(context_id,seq));
CREATE TABLE IF NOT EXISTS message_envelopes (message_id TEXT PRIMARY KEY REFERENCES messages(id), envelope TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS message_instances (message_id TEXT PRIMARY KEY REFERENCES messages(id), instance_id TEXT NOT NULL, label TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS instance_acks (session_id TEXT NOT NULL REFERENCES sessions(id), context_id TEXT NOT NULL REFERENCES contexts(id), instance_id TEXT NOT NULL, seq INTEGER NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(session_id,context_id,instance_id));
CREATE TABLE IF NOT EXISTS message_dedupe_scopes (context_id TEXT NOT NULL REFERENCES contexts(id), session_id TEXT NOT NULL REFERENCES sessions(id), instance_id TEXT NOT NULL, dedupe_key TEXT NOT NULL, message_id TEXT NOT NULL UNIQUE REFERENCES messages(id), PRIMARY KEY(context_id,session_id,instance_id,dedupe_key));
INSERT INTO message_dedupe_scopes SELECT m.context_id,m.session_id,COALESCE(i.instance_id,''),m.dedupe_key,m.id FROM messages m LEFT JOIN message_instances i ON i.message_id=m.id WHERE NOT EXISTS (SELECT 1 FROM message_dedupe_scopes d WHERE d.message_id=m.id);
CREATE TABLE IF NOT EXISTS message_filters (message_id TEXT PRIMARY KEY REFERENCES messages(id), context_id TEXT NOT NULL REFERENCES contexts(id), seq INTEGER NOT NULL, schema TEXT NOT NULL, task_id TEXT NOT NULL, sender TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS message_filters_schema ON message_filters(context_id,schema,seq);
CREATE INDEX IF NOT EXISTS message_filters_task ON message_filters(context_id,task_id,seq);
CREATE INDEX IF NOT EXISTS message_filters_sender ON message_filters(context_id,sender,seq);
CREATE TABLE IF NOT EXISTS message_recipients (message_id TEXT NOT NULL REFERENCES messages(id), context_id TEXT NOT NULL REFERENCES contexts(id), seq INTEGER NOT NULL, kind TEXT NOT NULL, recipient TEXT NOT NULL, PRIMARY KEY(message_id,kind,recipient));
CREATE INDEX IF NOT EXISTS message_recipients_lookup ON message_recipients(context_id,kind,recipient,seq);
INSERT OR REPLACE INTO message_filters SELECT m.id,m.context_id,m.seq,COALESCE(json_extract(e.envelope,'$.schema'),''),COALESCE(json_extract(e.envelope,'$.task_id'),''),COALESCE(i.instance_id,'') FROM messages m LEFT JOIN message_envelopes e ON e.message_id=m.id LEFT JOIN message_instances i ON i.message_id=m.id;
DELETE FROM message_recipients;
INSERT INTO message_recipients SELECT m.id,m.context_id,m.seq,'broadcast','' FROM messages m LEFT JOIN message_envelopes e ON e.message_id=m.id WHERE COALESCE(json_array_length(e.envelope,'$.recipient_instances'),0)=0 AND COALESCE(json_array_length(e.envelope,'$.recipient_session_ids'),0)=0;
INSERT INTO message_recipients SELECT DISTINCT m.id,m.context_id,m.seq,'session',j.value FROM messages m JOIN message_envelopes e ON e.message_id=m.id JOIN json_each(e.envelope,'$.recipient_session_ids') j WHERE COALESCE(json_array_length(e.envelope,'$.recipient_instances'),0)=0;
INSERT INTO message_recipients SELECT DISTINCT m.id,m.context_id,m.seq,'instance',j.value FROM messages m JOIN message_envelopes e ON e.message_id=m.id JOIN json_each(e.envelope,'$.recipient_instances') j;
CREATE TRIGGER IF NOT EXISTS comitia_message_filter_insert AFTER INSERT ON messages BEGIN
 INSERT INTO message_filters VALUES(NEW.id,NEW.context_id,NEW.seq,'','','');
 INSERT INTO message_recipients VALUES(NEW.id,NEW.context_id,NEW.seq,'broadcast','');
END;
CREATE TRIGGER IF NOT EXISTS comitia_message_envelope_filter_insert AFTER INSERT ON message_envelopes BEGIN
 UPDATE message_filters SET schema=COALESCE(json_extract(NEW.envelope,'$.schema'),''),task_id=COALESCE(json_extract(NEW.envelope,'$.task_id'),'') WHERE message_id=NEW.message_id;
 DELETE FROM message_recipients WHERE message_id=NEW.message_id;
 INSERT INTO message_recipients SELECT id,context_id,seq,'broadcast','' FROM messages WHERE id=NEW.message_id AND COALESCE(json_array_length(NEW.envelope,'$.recipient_instances'),0)=0 AND COALESCE(json_array_length(NEW.envelope,'$.recipient_session_ids'),0)=0;
 INSERT INTO message_recipients SELECT DISTINCT m.id,m.context_id,m.seq,'session',j.value FROM messages m JOIN json_each(NEW.envelope,'$.recipient_session_ids') j WHERE m.id=NEW.message_id AND COALESCE(json_array_length(NEW.envelope,'$.recipient_instances'),0)=0;
 INSERT INTO message_recipients SELECT DISTINCT m.id,m.context_id,m.seq,'instance',j.value FROM messages m JOIN json_each(NEW.envelope,'$.recipient_instances') j WHERE m.id=NEW.message_id;
END;
CREATE TRIGGER IF NOT EXISTS comitia_message_instance_filter_insert AFTER INSERT ON message_instances BEGIN
 UPDATE message_filters SET sender=NEW.instance_id WHERE message_id=NEW.message_id;
END;
CREATE TABLE IF NOT EXISTS acks (session_id TEXT NOT NULL REFERENCES sessions(id), context_id TEXT NOT NULL REFERENCES contexts(id), seq INTEGER NOT NULL, updated_at TEXT NOT NULL, PRIMARY KEY(session_id,context_id));
`

func (x *sqlStore) MessageByDedupe(cid, sid, key string, instances ...string) (Message, error) {
	instance, err := cursorInstance(instances)
	if err != nil {
		return Message{}, err
	}
	return scanMessage(x.q.QueryRow(messageSelect+" WHERE d.context_id=? AND d.session_id=? AND d.instance_id=? AND d.dedupe_key=?", cid, sid, instance, key))
}

const messageSelect = "SELECT m.id,m.context_id,m.session_id,m.seq,COALESCE(d.dedupe_key,m.dedupe_key),m.body,m.created_at,COALESCE(e.envelope,''),COALESCE(i.instance_id,''),COALESCE(i.label,''),COALESCE(a.task_id,''),COALESCE(a.lease_version,0),COALESCE(a.claim_token_hash,'') FROM messages m LEFT JOIN message_task_attempts a ON a.message_id=m.id LEFT JOIN message_envelopes e ON e.message_id=m.id LEFT JOIN message_instances i ON i.message_id=m.id LEFT JOIN message_dedupe_scopes d ON d.message_id=m.id"

func (x *sqlStore) Message(id string) (Message, error) {
	return scanMessage(x.q.QueryRow(messageSelect+" WHERE m.id=?", id))
}
func scanMessage(row interface{ Scan(...any) error }) (Message, error) {
	var m Message
	var c, envelope, instance, label string
	var attempt TaskAttempt
	e := row.Scan(&m.ID, &m.ContextID, &m.SessionID, &m.Sequence, &m.DedupeKey, &m.Body, &c, &envelope, &instance, &label, &attempt.TaskID, &attempt.LeaseVersion, &attempt.ClaimTokenHash)
	if e == nil && envelope != "" {
		e = json.Unmarshal([]byte(envelope), &m.Envelope)
	}
	if instance != "" {
		m.AgentInstance = &AgentInstance{ID: instance, Label: label}
		m.AgentInstanceSelfReported = true
	}
	if attempt.TaskID != "" {
		m.TaskAttempt = &attempt
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
	if e == nil && m.TaskAttempt != nil {
		a := m.TaskAttempt
		_, e = x.q.Exec("INSERT INTO message_task_attempts VALUES(?,?,?,?)", m.ID, a.TaskID, a.LeaseVersion, a.ClaimTokenHash)
	}
	return m, e
}

// Seek matching projections before loading bodies or decoding envelopes.
func messageQuery(cid, sid string, after int64, limit int, f MessageFilters) (string, []any) {
	query := messageSelect
	args := []any{cid, after}
	where, order := "m.context_id=? AND m.seq>?", "m.seq"
	index := ""
	switch {
	case f.TaskID != "":
		index = "message_filters_task"
	case f.FromInstance != "":
		index = "message_filters_sender"
	case f.Schema != "":
		index = "message_filters_schema"
	}
	if index != "" {
		query = strings.Replace(query, "FROM messages m", "FROM message_filters p INDEXED BY "+index+" CROSS JOIN messages m ON m.id=p.message_id", 1)
		where, order = "p.context_id=? AND p.seq>?", "p.seq"
		for _, field := range []struct{ column, value string }{{"task_id", f.TaskID}, {"sender", f.FromInstance}, {"schema", f.Schema}} {
			if field.value != "" {
				where += " AND p." + field.column + "=?"
				args = append(args, field.value)
			}
		}
		if f.ForInstance != "" {
			where += ` AND EXISTS (SELECT 1 FROM message_recipients r WHERE r.message_id=p.message_id AND ((r.kind='broadcast' AND r.recipient='') OR (r.kind='session' AND r.recipient=?) OR (r.kind='instance' AND r.recipient=?)))`
			args = append(args, sid, f.ForInstance)
		}
	} else if f.ForInstance != "" {
		// The routing classes are disjoint. Bound each ordered seek before
		// merging, so a small page does not materialize all eligible history.
		recipients := `SELECT message_id,seq FROM (SELECT message_id,seq FROM message_recipients WHERE context_id=? AND seq>? AND kind='broadcast' AND recipient='' ORDER BY seq LIMIT ?)
UNION ALL SELECT message_id,seq FROM (SELECT message_id,seq FROM message_recipients WHERE context_id=? AND seq>? AND kind='session' AND recipient=? ORDER BY seq LIMIT ?)
UNION ALL SELECT message_id,seq FROM (SELECT message_id,seq FROM message_recipients WHERE context_id=? AND seq>? AND kind='instance' AND recipient=? ORDER BY seq LIMIT ?)`
		recipientArgs := []any{cid, after, limit, cid, after, sid, limit, cid, after, f.ForInstance, limit}
		query = strings.Replace(query, "FROM messages m", "FROM ("+recipients+") p CROSS JOIN messages m ON m.id=p.message_id", 1)
		where, order, args = "1=1", "p.seq", recipientArgs
	}
	return query + " WHERE " + where + " ORDER BY " + order + " LIMIT ?", append(args, limit)
}

func (x *sqlStore) Messages(cid, callerSessionID string, after int64, limit int, filters ...MessageFilters) ([]Message, error) {
	f, err := messageFilters(filters)
	if err != nil {
		return nil, err
	}
	query, args := messageQuery(cid, callerSessionID, after, limit, f)
	rows, e := x.q.Query(query, args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var out []Message
	budget := messageByteBudget(f.MaxBytes)
	used := 2 // Array brackets.
	for rows.Next() {
		m, e := scanMessage(rows)
		if e != nil {
			return nil, e
		}
		raw, e := json.Marshal(m)
		if e != nil {
			return nil, e
		}
		n := len(raw)
		if len(out) > 0 {
			n++ // Comma.
			if used+n > budget {
				break
			}
		}
		out = append(out, m)
		used += n
	}
	return out, rows.Err()
}

// The empty scan and high-water mark share the caller's read transaction, so
// an append committed by another process cannot be skipped between them.
func (x *sqlStore) MessagesScanned(cid, sid string, after int64, limit int, f MessageFilters) ([]Message, int64, error) {
	messages, err := x.Messages(cid, sid, after, limit, f)
	if err != nil {
		return messages, after, err
	}
	if len(messages) != 0 {
		return messages, messages[len(messages)-1].Sequence, nil
	}
	var scanned int64
	err = x.q.QueryRow("SELECT COALESCE(MAX(seq),0) FROM messages WHERE context_id=?", cid).Scan(&scanned)
	return messages, max(after, scanned), err
}

func (x *sqlStore) Ack(sid, cid string, seq int64, at time.Time, instance string) error {
	var max int64
	if e := x.q.QueryRow("SELECT COALESCE(MAX(seq),0) FROM messages WHERE context_id=?", cid).Scan(&max); e != nil {
		return e
	}
	if seq > max {
		return Invalidf("sequence must not exceed the latest message sequence")
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
	return x.ParticipantsAfter(cid, now, "", limit)
}
func (x *sqlStore) ParticipantsAfter(cid string, now time.Time, afterID string, limit int) ([]Participant, error) {
	rows, err := x.q.Query("SELECT DISTINCT session_id FROM grants WHERE context_id=? AND session_id>? ORDER BY session_id", cid, afterID)
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
