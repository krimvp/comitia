package sqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krimvp/comitia/internal/model"
)

// Generation rows written by binaries whose message, hidden and compatibility
// schemas still embedded the projection backfills.
var combinedSchemaGenerations = map[string][]string{
	"main":              {"4dbf2144e79ac485e509d0862991ad6b007d5cbcdf2e9f4b7f8fa0057609c1ef", "ed52c2d26d5ba5458a6e8a615fc31a30a8ea60efdc49dbed242b87e660401f19", "2e56e77364176824cae00526d8f13f3ea12e31dffe0621a4c2a0c4648c1307ae", "5ec98994f50ff38965b9c72bfd246d6ce82d7ef983848af2ff4333939a79b7ff", "c6c6ac8938b352dbdeb90b9292697755f60b64c5a08baa5328fd8e6e0f546198", "a87564e078ddb594961fb5eaa4315118d218ec73935e009f73b497b9c78a53e8", "fd49c6f2b7108506de3e31254fa7211cf6aba2a38971affe136114419ae63986", "603eddc13f3d002600d599ffa87e5158a1bccd518e9039daf2580c655dfb09a9", "4f9ba9faf491ff72bd4193a88a787173f95c5652279f5c7309072d542abe1064"},
	"hidden-projection": {"99a639ebbf4690003437a2303c78ababb24af2b13fcdf558db2d4ad9a4f5f9f7", "389820f3d8dc387476739d406b5dc1bd87ef4bad3752c618a70a8196d88f7999", "94372ac1e5493458694e1425738304cb3e5ff60acab6593ec1cf3ea9e5d7d1d3", "5ec98994f50ff38965b9c72bfd246d6ce82d7ef983848af2ff4333939a79b7ff", "6e32fdc0a8a0d937e058f0083ce1dfcec923285191f618f4c2a0833cc7db16d0", "c6c6ac8938b352dbdeb90b9292697755f60b64c5a08baa5328fd8e6e0f546198", "a87564e078ddb594961fb5eaa4315118d218ec73935e009f73b497b9c78a53e8", "fd49c6f2b7108506de3e31254fa7211cf6aba2a38971affe136114419ae63986", "603eddc13f3d002600d599ffa87e5158a1bccd518e9039daf2580c655dfb09a9", "4f9ba9faf491ff72bd4193a88a787173f95c5652279f5c7309072d542abe1064"},
}

func populateProjections(t *testing.T, f fixture) {
	t.Helper()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	root, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "root", Body: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AppendMessage(ctx, f.admin.Token, f.context.ID, model.MessageInput{DedupeKey: "routed", Body: "routed", AgentInstance: &model.AgentInstance{ID: "worker"}, Envelope: &model.MessageEnvelope{Schema: "comitia.discussion.v1", TaskID: task.ID, ReplyTo: root.ID, RecipientInstances: []string{"reader"}, Payload: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	// A rebuild deletes every recipient row; a surviving marker proves none ran.
	if _, err := f.store.db.Exec("INSERT INTO message_recipients SELECT id,context_id,seq,'marker','' FROM messages LIMIT 1"); err != nil {
		t.Fatal(err)
	}
}

func assertProjectionsKept(t *testing.T, st *SQLiteStore) {
	t.Helper()
	var marker, filters, routed, replies int
	if err := st.db.QueryRow("SELECT (SELECT COUNT(*) FROM message_recipients WHERE kind='marker'),(SELECT COUNT(*) FROM message_filters),(SELECT COUNT(*) FROM message_recipients WHERE kind='instance' AND recipient='reader'),(SELECT COUNT(*) FROM message_replies)").Scan(&marker, &filters, &routed, &replies); err != nil {
		t.Fatal(err)
	}
	var messages int
	if err := st.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messages); err != nil {
		t.Fatal(err)
	}
	if marker != 1 || filters != messages || routed != 1 || replies != 1 {
		t.Fatalf("projections rebuilt or lost: marker=%d filters=%d/%d routed=%d replies=%d", marker, filters, messages, routed, replies)
	}
	if st.migrationPending(context.Background()) {
		t.Fatal("migration still pending")
	}
}

func TestSQLiteDDLEditDoesNotRerunBackfills(t *testing.T) {
	f := setup(t, filepath.Join(t.TempDir(), "state.db"))
	defer f.store.Close()
	populateProjections(t, f)
	if schemas[3] != messageSchema {
		t.Fatal("schemas[3] is not messageSchema")
	}
	old := schemas[3]
	defer func() { schemas[3] = old }()
	schemas[3] += "\nCREATE INDEX IF NOT EXISTS generation_test_index ON messages(created_at);"
	ctx := context.Background()
	if !f.store.migrationPending(ctx) {
		t.Fatal("schema change not detected")
	}
	if err := f.store.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertProjectionsKept(t, f.store)
	// Binaries from before the split find their combined generations and skip.
	for _, b := range backfills {
		for _, generation := range b.legacy {
			var exists bool
			if err := f.store.db.QueryRow("SELECT EXISTS(SELECT 1 FROM comitia_generations WHERE generation=?)", generation).Scan(&exists); err != nil || !exists {
				t.Fatalf("legacy generation %s not recorded: %v", generation, err)
			}
		}
	}
}

func TestSQLiteUpgradeFromCombinedGenerationsKeepsProjections(t *testing.T) {
	for name, generations := range combinedSchemaGenerations {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			f := setup(t, path)
			populateProjections(t, f)
			if _, err := f.store.db.Exec("DELETE FROM comitia_generations; PRAGMA user_version=0"); err != nil {
				t.Fatal(err)
			}
			for _, generation := range generations {
				if _, err := f.store.db.Exec("INSERT INTO comitia_generations VALUES(?)", generation); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.store.Close(); err != nil {
				t.Fatal(err)
			}
			st, err := OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			assertProjectionsKept(t, st)
			var version int
			if err := st.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != sqliteSchemaVersion {
				t.Fatalf("schema version %d: %v", version, err)
			}
		})
	}
}

func TestSQLiteRefusesNewerSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var version int
	if err := st.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != sqliteSchemaVersion {
		t.Fatalf("new database version %d: %v", version, err)
	}
	newer := sqliteSchemaVersion + 1
	if _, err := st.db.Exec(fmt.Sprintf("PRAGMA user_version=%d", newer)); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("schema version %d is newer than this binary supports (version %d)", newer, sqliteSchemaVersion)
	// A process that opened the file before the upgrade stops writing too.
	if err := st.Update(context.Background(), func(model.WriteStore) error { return nil }); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("open store wrote to newer database: %v", err)
	}
	if other, err := OpenSQLite(path); err == nil || !strings.Contains(err.Error(), want) {
		if other != nil {
			other.Close()
		}
		t.Fatalf("opened newer database: %v", err)
	}
}
