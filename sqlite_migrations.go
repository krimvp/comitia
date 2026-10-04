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
	schemas    = []string{sessionSchema, contextSchema, grantSchema, messageSchema, taskSchema, presenceSchema, contextTreeSchema, contextLifecycleSchema, compatibilitySchema}
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
INSERT OR IGNORE INTO comitia_message_repairs SELECT m.id FROM messages m WHERE NOT EXISTS (SELECT 1 FROM message_dedupe_scopes d WHERE d.message_id=m.id);
INSERT OR IGNORE INTO comitia_creator_repairs SELECT p.context_id FROM context_parents p WHERE NOT EXISTS (SELECT 1 FROM context_creators c WHERE c.context_id=p.context_id);
`

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

func schemaPending(ctx context.Context, q interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, schema string) bool {
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
	for _, repair := range []string{messageRepair, creatorRepair, "DELETE FROM comitia_message_repairs", "DELETE FROM comitia_creator_repairs"} {
		if _, err = tx.ExecContext(ctx, repair); err != nil {
			return err
		}
	}
	return tx.Commit()
}
