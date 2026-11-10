package comitia

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
)

var (
	schemas    = []string{sessionSchema, contextSchema, grantSchema, messageSchema, messageHiddenSchema, taskSchema, presenceSchema, contextTreeSchema, contextLifecycleSchema, compatibilitySchema}
	createStmt = regexp.MustCompile(`(?im)^CREATE\s+(?:UNIQUE\s+)?(TABLE|INDEX|TRIGGER|VIEW)\s+IF\s+NOT\s+EXISTS\s+(\w+)`)
)

// Triggers survive older binaries, which know neither generations nor side
// tables. Current writes clear their own repair entries in the same transaction.
const compatibilitySchema = `
CREATE TABLE IF NOT EXISTS comitia_generations (generation TEXT PRIMARY KEY);
CREATE TABLE IF NOT EXISTS comitia_message_repairs (message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE);
CREATE TABLE IF NOT EXISTS comitia_creator_repairs (context_id TEXT PRIMARY KEY REFERENCES contexts(id) ON DELETE CASCADE);
CREATE TRIGGER IF NOT EXISTS comitia_message_repair_enqueue AFTER INSERT ON messages BEGIN
 INSERT OR IGNORE INTO comitia_message_repairs VALUES(NEW.id);
END;
CREATE TRIGGER IF NOT EXISTS comitia_message_repair_complete AFTER INSERT ON message_dedupe_scopes BEGIN
 DELETE FROM comitia_message_repairs WHERE message_id=NEW.message_id;
END;
CREATE TRIGGER IF NOT EXISTS comitia_creator_repair_enqueue AFTER INSERT ON context_parents BEGIN
 INSERT OR IGNORE INTO comitia_creator_repairs VALUES(NEW.context_id);
END;
CREATE TRIGGER IF NOT EXISTS comitia_creator_grant_repair_enqueue AFTER INSERT ON grants BEGIN
 INSERT OR IGNORE INTO comitia_creator_repairs SELECT context_id FROM context_parents WHERE context_id=NEW.context_id AND NOT EXISTS (SELECT 1 FROM context_creators WHERE context_id=NEW.context_id);
END;
CREATE TRIGGER IF NOT EXISTS comitia_creator_repair_complete AFTER INSERT ON context_creators BEGIN
 DELETE FROM comitia_creator_repairs WHERE context_id=NEW.context_id;
END;
`

// Default reads skip hidden messages (renewed task events) by probing this
// projection instead of decoding envelopes. The trigger also covers rows
// written by older binaries.
const messageHiddenSchema = `
CREATE TABLE IF NOT EXISTS message_hidden (message_id TEXT PRIMARY KEY REFERENCES messages(id)) WITHOUT ROWID;
CREATE TRIGGER IF NOT EXISTS comitia_message_hidden_insert AFTER INSERT ON message_envelopes WHEN json_extract(NEW.envelope,'$.schema')='` + TaskEventSchema + `' AND json_extract(NEW.envelope,'$.payload.kind')='renewed' BEGIN
 INSERT OR IGNORE INTO message_hidden VALUES(NEW.message_id);
END;
`

// Backfills derive projections from history. Schemas hold only idempotent DDL,
// so DDL edits never rescan history. A backfill runs once per its own
// generation, and again whenever an object it fills or whose trigger keeps it
// current was missing, since writes may have bypassed it. Legacy generations
// are the combined DDL-and-backfill schema strings that already ran it; they
// are recorded too, so binaries from before the split skip their rebuilds.
type backfill struct {
	sql     string
	objects []string
	legacy  []string
}

var backfills = []backfill{{
	sql: `INSERT OR REPLACE INTO message_filters SELECT m.id,m.context_id,m.seq,COALESCE(json_extract(e.envelope,'$.schema'),''),COALESCE(json_extract(e.envelope,'$.task_id'),''),COALESCE(i.instance_id,'') FROM messages m LEFT JOIN message_envelopes e ON e.message_id=m.id LEFT JOIN message_instances i ON i.message_id=m.id;
DELETE FROM message_recipients;
INSERT INTO message_recipients SELECT m.id,m.context_id,m.seq,'broadcast','' FROM messages m LEFT JOIN message_envelopes e ON e.message_id=m.id WHERE COALESCE(json_array_length(e.envelope,'$.recipient_instances'),0)=0 AND COALESCE(json_array_length(e.envelope,'$.recipient_session_ids'),0)=0;
INSERT INTO message_recipients SELECT DISTINCT m.id,m.context_id,m.seq,'session',j.value FROM messages m JOIN message_envelopes e ON e.message_id=m.id JOIN json_each(e.envelope,'$.recipient_session_ids') j WHERE COALESCE(json_array_length(e.envelope,'$.recipient_instances'),0)=0;
INSERT INTO message_recipients SELECT DISTINCT m.id,m.context_id,m.seq,'instance',j.value FROM messages m JOIN message_envelopes e ON e.message_id=m.id JOIN json_each(e.envelope,'$.recipient_instances') j;
INSERT OR REPLACE INTO message_replies SELECT m.id,m.context_id,m.seq,json_extract(e.envelope,'$.reply_to') FROM messages m JOIN message_envelopes e ON e.message_id=m.id WHERE COALESCE(json_extract(e.envelope,'$.reply_to'),'')<>'';`,
	objects: []string{"message_filters", "message_recipients", "message_replies", "comitia_message_filter_insert", "comitia_message_envelope_filter_insert", "comitia_message_instance_filter_insert", "comitia_message_reply_insert"},
	legacy:  []string{"5ec98994f50ff38965b9c72bfd246d6ce82d7ef983848af2ff4333939a79b7ff"},
}, {
	// Reads the schema projection, so it follows the projection backfill.
	sql:     `INSERT OR IGNORE INTO message_hidden SELECT e.message_id FROM message_filters p CROSS JOIN message_envelopes e ON e.message_id=p.message_id WHERE p.schema='` + TaskEventSchema + `' AND json_extract(e.envelope,'$.payload.kind')='renewed';`,
	objects: []string{"message_hidden", "comitia_message_hidden_insert"},
	legacy:  []string{"6e32fdc0a8a0d937e058f0083ce1dfcec923285191f618f4c2a0833cc7db16d0"},
}, {
	// Queue history that no trigger saw; messageRepair drains it.
	sql:     `INSERT OR IGNORE INTO comitia_message_repairs SELECT m.id FROM messages m WHERE NOT EXISTS (SELECT 1 FROM message_dedupe_scopes d WHERE d.message_id=m.id);`,
	objects: []string{"message_dedupe_scopes", "comitia_message_repairs", "comitia_message_repair_enqueue"},
	legacy:  []string{"4f9ba9faf491ff72bd4193a88a787173f95c5652279f5c7309072d542abe1064"},
}, {
	// Likewise for creatorRepair. contextLifecycleSchema once filled
	// context_creators directly, which is an equivalent completed run.
	sql:     `INSERT OR IGNORE INTO comitia_creator_repairs SELECT p.context_id FROM context_parents p WHERE NOT EXISTS (SELECT 1 FROM context_creators c WHERE c.context_id=p.context_id);`,
	objects: []string{"context_creators", "comitia_creator_repairs", "comitia_creator_repair_enqueue", "comitia_creator_grant_repair_enqueue"},
	legacy:  []string{"4f9ba9faf491ff72bd4193a88a787173f95c5652279f5c7309072d542abe1064", "603eddc13f3d002600d599ffa87e5158a1bccd518e9039daf2580c655dfb09a9"},
}}

func (b backfill) generations() []string {
	return append([]string{schemaGeneration(b.sql)}, b.legacy...)
}

func backfillPending(ctx context.Context, q queryRower, b backfill) bool {
	var done, exists bool
	for _, generation := range b.generations() {
		if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM comitia_generations WHERE generation=?)", generation).Scan(&exists); err != nil {
			return true
		}
		done = done || exists
	}
	if !done {
		return true
	}
	for _, name := range b.objects {
		if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name=?)", name).Scan(&exists); err != nil || !exists {
			return true
		}
	}
	return false
}

const messageRepair = `INSERT INTO message_dedupe_scopes
SELECT m.context_id,m.session_id,COALESCE(i.instance_id,''),m.dedupe_key,m.id
FROM comitia_message_repairs r CROSS JOIN messages m ON m.id=r.message_id
LEFT JOIN message_instances i ON i.message_id=m.id
WHERE NOT EXISTS (SELECT 1 FROM message_dedupe_scopes d WHERE d.message_id=m.id)`

const creatorRepair = `INSERT OR IGNORE INTO context_creators
SELECT p.context_id,g.id,g.parent_grant_id
FROM comitia_creator_repairs r CROSS JOIN context_parents p ON p.context_id=r.context_id
JOIN grants g ON g.id=(
 SELECT g2.id FROM grants g2 JOIN grants source ON source.id=g2.parent_grant_id
 WHERE g2.context_id=p.context_id AND g2.session_id=p.creator_session_id AND g2.issuer_session_id=p.creator_session_id
 AND source.context_id=p.parent_context_id ORDER BY g2.created_at,g2.id LIMIT 1
)`

func schemaGeneration(schema string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(schema))) }

type queryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func schemaPending(ctx context.Context, q queryRower, schema string) bool {
	var exists bool
	if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM comitia_generations WHERE generation=?)", schemaGeneration(schema)).Scan(&exists); err != nil || !exists {
		return true
	}
	// Generations do not hide removed objects. This also detects additive
	// index statements supplied by other schema owners on future upgrades.
	for _, m := range createStmt.FindAllStringSubmatch(schema, -1) {
		if err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type=? AND name=?)", strings.ToLower(m[1]), m[2]).Scan(&exists); err != nil || !exists {
			return true
		}
	}
	return false
}

func (s *SQLiteStore) migrationPending(ctx context.Context) bool {
	for _, schema := range schemas {
		if schemaPending(ctx, s.db, schema) {
			return true
		}
	}
	for _, b := range backfills {
		if backfillPending(ctx, s.db, b) {
			return true
		}
	}
	var pending bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM comitia_message_repairs) OR EXISTS(SELECT 1 FROM comitia_creator_repairs)").Scan(&pending)
	return err != nil || pending
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	// Every CLI call opens the store. Unchanged generations and empty indexed
	// repair queues are probed without a write lock or a historical data scan.
	if !s.migrationPending(ctx) {
		return nil
	}
	select {
	case s.writers <- struct{}{}:
		defer func() { <-s.writers }()
	case <-ctx.Done():
		return ctx.Err()
	}
	tx, err := s.begin(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS comitia_generations (generation TEXT PRIMARY KEY)"); err != nil {
		return err
	}
	// Recheck under the writer lock: another process may have migrated first.
	// Decide before DDL recreates missing objects that the backfill must fill.
	needed := make([]bool, len(backfills))
	for i, b := range backfills {
		needed[i] = backfillPending(ctx, tx, b)
	}
	for _, schema := range schemas {
		if !schemaPending(ctx, tx, schema) {
			continue
		}
		if _, err = tx.ExecContext(ctx, schema); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO comitia_generations VALUES(?)", schemaGeneration(schema)); err != nil {
			return err
		}
	}
	for i, b := range backfills {
		if !needed[i] {
			continue
		}
		if _, err = tx.ExecContext(ctx, b.sql); err != nil {
			return err
		}
		for _, generation := range b.generations() {
			if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO comitia_generations VALUES(?)", generation); err != nil {
				return err
			}
		}
	}
	for _, repair := range []string{messageRepair, creatorRepair, "DELETE FROM comitia_message_repairs", "DELETE FROM comitia_creator_repairs"} {
		if _, err = tx.ExecContext(ctx, repair); err != nil {
			return err
		}
	}
	return tx.Commit()
}
