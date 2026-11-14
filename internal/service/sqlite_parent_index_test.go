package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/sqlstore"

	"github.com/krimvp/comitia/internal/model"
)

func queryPlan(t *testing.T, st *sqlstore.SQLiteStore, query string, args ...any) string {
	t.Helper()
	rows, err := sqlstore.DB(st).Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, "\n")
}

func TestRevokeCascadesSeekParentIndexes(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	coord, g := delegate(t, f, f.admin.Token, "coord", f.context.ID, model.PermissionRead|model.PermissionGrant, time.Hour)
	delegate(t, f, coord.Token, "worker", f.context.ID, model.PermissionRead, time.Hour)
	for _, tc := range []struct{ query, root, index string }{
		{sqlstore.RevokeSessionTreeSQL, coord.Session.ID, "sessions_parent"},
		{sqlstore.RevokeGrantTreeSQL, g.ID, "grants_parent"},
	} {
		plan := queryPlan(t, f.store, tc.query, tc.root, model.TS(*f.now))
		if !strings.Contains(plan, tc.index) || strings.Contains(plan, "AUTOMATIC") {
			t.Fatalf("revoke cascade does not seek %s:\n%s", tc.index, plan)
		}
	}
}

// A database written before the parent indexes existed gains them on open,
// including when several processes open it at once.
func TestParentIndexesAddedToPopulatedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	f := setup(t, path)
	coord, g := delegate(t, f, f.admin.Token, "coord", f.context.ID, model.PermissionRead|model.PermissionGrant, time.Hour)
	worker, _ := delegate(t, f, coord.Token, "worker", f.context.ID, model.PermissionRead, time.Hour)
	old := map[int]string{
		0: strings.Replace(sqlstore.SessionSchemaSQL, "CREATE INDEX IF NOT EXISTS sessions_parent ON sessions(parent_id);\n", "", 1),
		2: strings.Replace(sqlstore.GrantSchemaSQL, "CREATE INDEX IF NOT EXISTS grants_parent ON grants(parent_grant_id);\n", "", 1),
	}
	for i, schema := range old {
		if schema == sqlstore.Schemas[i] {
			t.Fatalf("schema %d has no parent index", i)
		}
		if _, err := sqlstore.DB(f.store).Exec("DELETE FROM comitia_generations WHERE generation=?", sqlstore.SchemaGeneration(sqlstore.Schemas[i])); err != nil {
			t.Fatal(err)
		}
		if _, err := sqlstore.DB(f.store).Exec("INSERT INTO comitia_generations VALUES(?)", sqlstore.SchemaGeneration(schema)); err != nil {
			t.Fatal(err)
		}
	}
	for _, stmt := range []string{"DROP INDEX sessions_parent", "DROP INDEX grants_parent"} {
		if _, err := sqlstore.DB(f.store).Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if !sqlstore.MigrationPending(f.store, context.Background()) {
		t.Fatal("missing parent indexes not detected")
	}
	f.store.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, err := sqlstore.OpenSQLite(path)
			if err != nil {
				errs <- fmt.Errorf("open %d: %w", i, err)
				return
			}
			st.Close()
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	st, err := sqlstore.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if sqlstore.MigrationPending(st, context.Background()) {
		t.Fatal("migration still pending")
	}
	var n int
	if err = sqlstore.DB(st).QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name IN ('sessions_parent','grants_parent')").Scan(&n); err != nil || n != 2 {
		t.Fatalf("parent indexes=%d err=%v", n, err)
	}
	if !strings.Contains(queryPlan(t, st, sqlstore.RevokeGrantTreeSQL, g.ID, model.TS(*f.now)), "grants_parent") {
		t.Fatal("upgraded database does not seek grants_parent")
	}
	if err = st.Update(context.Background(), func(w model.WriteStore) error { return w.RevokeSessionTree(coord.Session.ID, *f.now) }); err != nil {
		t.Fatal(err)
	}
	if err = st.View(context.Background(), func(r model.ReadStore) error {
		s, err := r.Session(worker.Session.ID)
		if err == nil && s.RevokedAt == nil {
			err = fmt.Errorf("descendant not revoked")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
