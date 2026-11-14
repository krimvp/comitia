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
CREATE TABLE IF NOT EXISTS message_filters (message_id TEXT PRIMARY KEY REFERENCES messages(id), context_id TEXT NOT NULL REFERENCES contexts(id), seq INTEGER NOT NULL, schema TEXT NOT NULL, task_id TEXT NOT NULL, sender TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS message_filters_schema ON message_filters(context_id,schema,seq);
CREATE INDEX IF NOT EXISTS message_filters_task ON message_filters(context_id,task_id,seq);
CREATE INDEX IF NOT EXISTS message_filters_sender ON message_filters(context_id,sender,seq);
CREATE TABLE IF NOT EXISTS message_recipients (message_id TEXT NOT NULL REFERENCES messages(id), context_id TEXT NOT NULL REFERENCES contexts(id), seq INTEGER NOT NULL, kind TEXT NOT NULL, recipient TEXT NOT NULL, PRIMARY KEY(message_id,kind,recipient));
CREATE INDEX IF NOT EXISTS message_recipients_lookup ON message_recipients(context_id,kind,recipient,seq);
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
CREATE TABLE IF NOT EXISTS message_replies (message_id TEXT PRIMARY KEY REFERENCES messages(id), context_id TEXT NOT NULL, seq INTEGER NOT NULL, reply_to TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS message_replies_lookup ON message_replies(context_id,reply_to,seq);
CREATE INDEX IF NOT EXISTS messages_author ON messages(context_id,session_id,seq);
CREATE TRIGGER IF NOT EXISTS comitia_message_reply_insert AFTER INSERT ON message_envelopes WHEN COALESCE(json_extract(NEW.envelope,'$.reply_to'),'')<>'' BEGIN
 INSERT INTO message_replies SELECT id,context_id,seq,json_extract(NEW.envelope,'$.reply_to') FROM messages WHERE id=NEW.message_id;
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
	case f.Schema != "", len(f.Schemas) != 0:
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
			if f.StrictRecipients {
				where += " AND " + strictRecipientSessions("e.envelope")
				args = append(args, sid)
			}
		}
	} else if f.ForInstance != "" && !scopedMessageFilters(f) {
		// The routing classes are disjoint. Bound each ordered seek before
		// merging, so a small page does not materialize all eligible history.
		recipients := `SELECT message_id,seq FROM (SELECT message_id,seq FROM message_recipients WHERE context_id=? AND seq>? AND kind='broadcast' AND recipient='' ORDER BY seq LIMIT ?)
UNION ALL SELECT message_id,seq FROM (SELECT message_id,seq FROM message_recipients WHERE context_id=? AND seq>? AND kind='session' AND recipient=? ORDER BY seq LIMIT ?)
UNION ALL SELECT message_id,seq FROM (SELECT message_id,seq FROM message_recipients WHERE context_id=? AND seq>? AND kind='instance' AND recipient=? ORDER BY seq LIMIT ?)`
		recipientArgs := []any{cid, after, limit, cid, after, sid, limit, cid, after, f.ForInstance}
		if f.StrictRecipients {
			recipients = strings.Replace(recipients, "kind='instance' AND recipient=? ORDER BY seq", "kind='instance' AND recipient=? AND "+strictRecipientSessions("(SELECT envelope FROM message_envelopes WHERE message_id=message_recipients.message_id)")+" ORDER BY seq", 1)
			recipientArgs = append(recipientArgs, sid)
		}
		recipientArgs = append(recipientArgs, limit)
		// Apply selectors inside each seek before bounding candidates.
		predicate, predicateArgs := messageReadPredicate(sid, f, "message_recipients.message_id")
		if predicate != "" {
			recipients = strings.ReplaceAll(recipients, " ORDER BY seq LIMIT ?", " AND "+predicate+" ORDER BY seq LIMIT ?")
			var argsWithFilters []any
			for _, arg := range recipientArgs {
				if _, isLimit := arg.(int); isLimit {
					argsWithFilters = append(argsWithFilters, predicateArgs...)
				}
				argsWithFilters = append(argsWithFilters, arg)
			}
			recipientArgs = argsWithFilters
		}
		query = strings.Replace(query, "FROM messages m", "FROM ("+recipients+") p CROSS JOIN messages m ON m.id=p.message_id", 1)
		where, order, args = "1=1", "p.seq", recipientArgs
	} else if f.ForInstance != "" {
		// Scoped selectors below must run before any limit. Bounding recipient
		// candidates first could omit a later match and falsely advance the scan.
		where += ` AND EXISTS (SELECT 1 FROM message_recipients r WHERE r.message_id=m.id AND ((r.kind='broadcast' AND r.recipient='') OR (r.kind='session' AND r.recipient=?) OR (r.kind='instance' AND r.recipient=?)))`
		args = append(args, sid, f.ForInstance)
		if f.StrictRecipients {
			where += " AND " + strictRecipientSessions("e.envelope")
			args = append(args, sid)
		}
	}
	if f.ExcludeSelf {
		where += " AND NOT (m.session_id=? AND COALESCE(i.instance_id,'')=?)"
		args = append(args, sid, filterAgentInstance(f))
	}
	if len(f.Schemas) != 0 {
		// Any schema list selects the projection seek above.
		where += " AND p.schema IN (" + strings.TrimSuffix(strings.Repeat("?,", len(f.Schemas)), ",") + ")"
		for _, schema := range f.Schemas {
			args = append(args, schema)
		}
	}
	if !filterIncludesRenewals(f) {
		where += " AND NOT EXISTS (SELECT 1 FROM message_hidden h WHERE h.message_id=m.id)"
	}
	if f.FromSession != "" {
		where += " AND m.session_id=?"
		args = append(args, f.FromSession)
	}
	if f.ReplyTo != "" {
		where += " AND m.id IN (SELECT message_id FROM message_replies WHERE context_id=? AND reply_to=?)"
		args = append(args, cid, f.ReplyTo)
	}
	if f.ThreadID != "" {
		// Drive each recursion step from the frontier so it seeks replies by
		// (context_id,reply_to) instead of rescanning the whole context.
		query = "WITH RECURSIVE thread(id) AS (SELECT id FROM messages WHERE context_id=? AND id=? UNION ALL SELECT r.message_id FROM thread t CROSS JOIN message_replies r ON r.reply_to=t.id WHERE r.context_id=?) " + query
		args = append([]any{cid, f.ThreadID, cid}, args...)
		where += " AND m.id IN (SELECT id FROM thread)"
	}
	return query + " WHERE " + where + " ORDER BY " + order + " LIMIT ?", append(args, limit)
}

// Instance candidates already match the selected instance. Empty session lists
// are unrestricted; compound addresses must also name the authenticated caller.
func strictRecipientSessions(envelope string) string {
	return "(NOT EXISTS (SELECT 1 FROM json_each(" + envelope + ", '$.recipient_session_ids')) OR EXISTS (SELECT 1 FROM json_each(" + envelope + ", '$.recipient_session_ids') WHERE value=?))"
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
	// Effective access is decided per session, so holders are read in
	// limit-sized chunks until the page is full instead of all at once.
	chunk := limit
	if chunk <= 0 {
		chunk = -1
	}
	out := []Participant{}
	for {
		ids, err := x.participantCandidates(cid, afterID, chunk)
		if err != nil {
			return nil, err
		}
		if len(ids) == 0 {
			return out, nil
		}
		afterID = ids[len(ids)-1]
		for _, sid := range ids {
			p, ok, err := x.participant(cid, sid, now)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			out = append(out, p)
			if len(out) == limit {
				return out, nil
			}
		}
		if chunk < 0 || len(ids) < chunk {
			return out, nil
		}
	}
}

func (x *sqlStore) participantCandidates(cid, afterID string, limit int) ([]string, error) {
	rows, err := x.q.Query("SELECT DISTINCT session_id FROM grants WHERE context_id=? AND session_id>? ORDER BY session_id LIMIT ?", cid, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var sid string
		if err = rows.Scan(&sid); err != nil {
			return nil, err
		}
		ids = append(ids, sid)
	}
	return ids, rows.Err()
}

func (x *sqlStore) participant(cid, sid string, now time.Time) (Participant, bool, error) {
	session, err := x.Session(sid)
	if err != nil || session.RevokedAt != nil || !session.ExpiresAt.After(now) {
		return Participant{}, false, err
	}
	grants, err := x.Grants(sid, cid, now)
	if err != nil {
		return Participant{}, false, err
	}
	for _, g := range grants {
		if g.Permissions&PermissionRead != 0 {
			principal, err := x.Principal(session.PrincipalID)
			if err != nil {
				return Participant{}, false, err
			}
			return Participant{SessionID: sid, PrincipalID: principal.ID, Name: principal.Name, ExpiresAt: session.ExpiresAt}, true, nil
		}
	}
	return Participant{}, false, nil
}

// Recipient seeks must apply these selectors before their per-class limit.
func messageReadPredicate(sid string, f MessageFilters, mid string) (string, []any) {
	var predicates []string
	var args []any
	if f.ExcludeSelf {
		predicates = append(predicates, "NOT EXISTS (SELECT 1 FROM messages m LEFT JOIN message_instances i ON i.message_id=m.id WHERE m.id="+mid+" AND m.session_id=? AND COALESCE(i.instance_id,'')=?)")
		args = append(args, sid, filterAgentInstance(f))
	}
	if len(f.Schemas) != 0 {
		predicates = append(predicates, "EXISTS (SELECT 1 FROM message_filters f WHERE f.message_id="+mid+" AND f.schema IN ("+strings.TrimSuffix(strings.Repeat("?,", len(f.Schemas)), ",")+"))")
		for _, schema := range f.Schemas {
			args = append(args, schema)
		}
	}
	if !filterIncludesRenewals(f) {
		predicates = append(predicates, "NOT EXISTS (SELECT 1 FROM message_hidden h WHERE h.message_id="+mid+")")
	}
	return strings.Join(predicates, " AND "), args
}
