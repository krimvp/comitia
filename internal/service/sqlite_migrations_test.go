package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/sqlstore"

	"github.com/krimvp/comitia/internal/model"
)

func TestSQLiteLegacyCreatorRepairIncludingLateGrant(t *testing.T) {
	for _, order := range []string{"parent-first", "grant-first", "late-grant"} {
		t.Run(order, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			f := setup(t, path)
			defer func() { f.store.Close() }()
			ctx := context.Background()
			coord, source := delegate(t, f, f.admin.Token, "coord", f.context.ID, model.PermissionRead|model.PermissionCreate, time.Hour)
			c := model.WorkContext{ID: "legacy-child", WorkspaceID: f.context.WorkspaceID, Name: "legacy", CreatedAt: *f.now, ParentContextID: f.context.ID, CreatorSessionID: coord.Session.ID}
			g := model.Grant{ID: "legacy-grant", SessionID: coord.Session.ID, ContextID: c.ID, ParentGrantID: source.ID, IssuerSessionID: coord.Session.ID, Permissions: childPermissions, ExpiresAt: source.ExpiresAt, CreatedAt: *f.now}
			if err := f.store.Update(ctx, func(w model.WriteStore) error {
				if order != "grant-first" {
					if err := w.PutContext(c); err != nil {
						return err
					}
					if order == "late-grant" {
						return nil
					}
					return w.PutGrant(g)
				}
				root := c
				root.ParentContextID = ""
				if err := w.PutContext(root); err != nil {
					return err
				}
				if err := w.PutGrant(g); err != nil {
					return err
				}
				_, err := sqlstore.TxQuerier(w.(*sqlstore.TxStore)).Exec("INSERT INTO context_parents VALUES(?,?,?,?,?)", c.ID, c.ParentContextID, nil, c.CreatorSessionID, false)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			reopen := func() {
				t.Helper()
				if err := f.store.Close(); err != nil {
					t.Fatal(err)
				}
				var err error
				f.store, err = sqlstore.OpenSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
			}
			reopen()
			if order == "late-grant" {
				// No matching grant is a completed repair attempt, not work to
				// repeat on every open. A later legacy grant must requeue it.
				if sqlstore.MigrationPending(f.store, ctx) {
					t.Fatal("unrepairable child causes repeated migration")
				}
				if err := f.store.Update(ctx, func(w model.WriteStore) error { return w.PutGrant(g) }); err != nil {
					t.Fatal(err)
				}
				reopen()
			}
			var creator, creating string
			if err := sqlstore.DB(f.store).QueryRow("SELECT creator_grant_id,creating_grant_id FROM context_creators WHERE context_id=?", c.ID).Scan(&creator, &creating); err != nil || creator != g.ID || creating != source.ID {
				t.Fatalf("legacy creator: %q %q %v", creator, creating, err)
			}
			if sqlstore.MigrationPending(f.store, ctx) {
				t.Fatal("completed legacy repair repeats")
			}
		})
	}
}

func TestSQLiteRepairQueueSchemaRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	f := setup(t, path)
	defer func() { f.store.Close() }()
	ctx := context.Background()
	// A missing trigger must not hide writes that happened while it was gone.
	if _, err := sqlstore.DB(f.store).Exec("DROP TRIGGER comitia_message_repair_enqueue"); err != nil {
		t.Fatal(err)
	}
	if _, err := sqlstore.DB(f.store).Exec("INSERT INTO messages VALUES(?,?,?,?,?,?,?)", "legacy", f.context.ID, f.admin.Session.ID, 1, "legacy", "legacy", model.TS(*f.now)); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	f.store, err = sqlstore.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.View(ctx, func(r model.ReadStore) error {
		_, err := r.MessageByDedupe(f.context.ID, f.admin.Session.ID, "legacy")
		return err
	}); err != nil {
		t.Fatalf("missing-trigger legacy repair: %v", err)
	}
	if sqlstore.MigrationPending(f.store, ctx) {
		t.Fatal("recovered schema reports pending work")
	}
}

func TestSQLiteMigrationGenerationDetectsAddedIndex(t *testing.T) {
	st, err := sqlstore.OpenSQLite(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	old := sqlstore.Schemas[2]
	defer func() { sqlstore.Schemas[2] = old }()
	sqlstore.Schemas[2] += "\nCREATE INDEX IF NOT EXISTS generation_test_index ON grants(context_id,session_id);"
	ctx := context.Background()
	if !sqlstore.MigrationPending(st, ctx) {
		t.Fatal("schema change not detected")
	}
	if err := sqlstore.Migrate(st, ctx); err != nil || sqlstore.MigrationPending(st, ctx) {
		t.Fatalf("new generation: %v", err)
	}
	if _, err := sqlstore.DB(st).Exec("DROP INDEX generation_test_index"); err != nil {
		t.Fatal(err)
	}
	if !sqlstore.MigrationPending(st, ctx) {
		t.Fatal("generation hid missing index")
	}
	if err := sqlstore.Migrate(st, ctx); err != nil || sqlstore.MigrationPending(st, ctx) {
		t.Fatalf("repair missing index: %v", err)
	}
}

func TestSQLiteIncrementalRepairUsesQueuedKeys(t *testing.T) {
	st, err := sqlstore.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Lock the join order to the queue. Cost must depend on newly queued keys,
	// even if ANALYZE makes SQLite prefer a historical table as the outer loop.
	for _, repair := range []string{sqlstore.MessageRepairSQL, sqlstore.CreatorRepairSQL} {
		rows, err := sqlstore.DB(st).Query("EXPLAIN QUERY PLAN " + repair)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(plan, "\n")
		if !strings.Contains(joined, "SCAN r") || strings.Contains(joined, "SCAN m") || strings.Contains(joined, "SCAN p") || strings.Contains(joined, "SCAN g") {
			t.Fatalf("repair scans history instead of queued keys:\n%s", joined)
		}
	}
}

func TestSQLiteLegacyRepairBatchIsAtomicAndResumable(t *testing.T) {
	f := setup(t, filepath.Join(t.TempDir(), "state.db"))
	defer f.store.Close()
	ctx := context.Background()
	first, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "first", Body: "first", AgentInstance: &model.AgentInstance{ID: "worker"}})
	if err != nil {
		t.Fatal(err)
	}
	// Queue multiple legacy messages, ending with a conflicting logical key.
	// A failed repair must leave the entire batch pending, not just the last row.
	const batch = 50
	if err := f.store.Update(ctx, func(w model.WriteStore) error {
		q := sqlstore.TxQuerier(w.(*sqlstore.TxStore))
		for i := 0; i <= batch; i++ {
			mid := fmt.Sprintf("legacy-%d", i)
			key := mid
			if i == batch {
				key = "first"
			}
			// Free the physical legacy tuple but keep the existing logical scope.
			if i == 0 {
				if _, err := q.Exec("UPDATE messages SET dedupe_key=? WHERE id=?", first.ID, first.ID); err != nil {
					return err
				}
			}
			if _, err := q.Exec("INSERT INTO messages VALUES(?,?,?,?,?,?,?)", mid, f.context.ID, f.admin.Session.ID, i+2, key, "legacy", model.TS(*f.now)); err != nil {
				return err
			}
			if _, err := q.Exec("INSERT INTO message_instances VALUES(?,?,?)", mid, "worker", "legacy worker"); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.Migrate(f.store, ctx); err == nil {
		t.Fatal("conflicting legacy scope was silently discarded")
	}
	var scopes, queued int
	if err := sqlstore.DB(f.store).QueryRow("SELECT COUNT(*) FROM message_dedupe_scopes").Scan(&scopes); err != nil || scopes != 1 {
		t.Fatalf("failed repair partially committed scopes: %d %v", scopes, err)
	}
	if err := sqlstore.DB(f.store).QueryRow("SELECT COUNT(*) FROM comitia_message_repairs").Scan(&queued); err != nil || queued != batch+1 {
		t.Fatalf("failed repair consumed queue: %d %v", queued, err)
	}
	// Removing the conflicting duplicate permits a retry of all remaining rows.
	if err := f.store.Update(ctx, func(w model.WriteStore) error {
		q := sqlstore.TxQuerier(w.(*sqlstore.TxStore))
		mid := fmt.Sprintf("legacy-%d", batch)
		for _, table := range []string{"message_filters", "message_recipients", "message_instances"} {
			if _, err := q.Exec("DELETE FROM "+table+" WHERE message_id=?", mid); err != nil {
				return err
			}
		}
		_, err := q.Exec("DELETE FROM messages WHERE id=?", mid)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := sqlstore.Migrate(f.store, ctx); err != nil || sqlstore.MigrationPending(f.store, ctx) {
		t.Fatalf("repair retry: %v", err)
	}
	if err := sqlstore.DB(f.store).QueryRow("SELECT COUNT(*) FROM message_dedupe_scopes").Scan(&scopes); err != nil || scopes != batch+1 {
		t.Fatalf("batch repair skipped rows: %d %v", scopes, err)
	}
}
