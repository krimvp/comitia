package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/sqlite"
)

// TestRemoteBusyStorageExitsUnavailable runs the real CLI against a server
// whose SQLite store is busy: another connection holds the write lock. Busy
// storage exits 7 in embedded mode, and must in remote mode too.
func TestRemoteBusyStorageExitsUnavailable(t *testing.T) {
	db := filepath.Join(t.TempDir(), "busy.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	owner := boot["credential"].(map[string]any)["Token"].(string)
	w := command(t, "--db", db, "--credential", owner, "workspace-create", "--name", "w")
	c := command(t, "--db", db, "--credential", owner, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")

	store, err := sqlite.OpenSQLite(db)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	api, err := comitia.New(comitia.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(httpapi.NewHandler(api))
	defer server.Close()

	lock, err := sql.Open("sqlite", (&url.URL{Scheme: "file", Path: db}).String())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	conn, err := lock.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK")

	// Each write retries the busy lock for 15 seconds, so both modes run at once.
	modes := map[string][]string{"embedded": {"--db", db}, "remote": {"--server-url", server.URL}}
	type result struct {
		mode, stderr string
		code         int
	}
	results := make(chan result, len(modes))
	for mode, args := range modes {
		go func() {
			args := append(append([]string{}, args...), "--credential", owner, "task-create", "--context-id", c["ID"].(string), "--title", "t")
			_, stderr, code := cli(t, args...)
			results <- result{mode, stderr, code}
		}()
	}
	for range modes {
		r := <-results
		var e map[string]string
		if r.code != exitUnavailable || json.Unmarshal([]byte(r.stderr), &e) != nil || e["error"] == "" {
			t.Errorf("%s: exit %d, want %d: %s", r.mode, r.code, exitUnavailable, r.stderr)
		}
	}
}
