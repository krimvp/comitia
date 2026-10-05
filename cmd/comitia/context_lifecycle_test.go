package main

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
)

func TestContextArchiveCommand(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded", true: "remote"}[remote], func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "archive.db")
			boot := command(t, "--db", db, "--bootstrap-credential", "s", "bootstrap")
			cred := boot["credential"].(map[string]any)["Token"].(string)
			w := command(t, "--db", db, "--credential", cred, "workspace-create", "--name", "w")
			cid := command(t, "--db", db, "--credential", cred, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")["ID"].(string)
			base := []string{"--db", db, "--credential", cred}
			if remote {
				api, closeFn, err := open(config{db: db})
				if err != nil {
					t.Fatal(err)
				}
				defer closeFn()
				ts := httptest.NewServer(httpapi.NewHandler(api))
				defer ts.Close()
				base = []string{"--server-url", ts.URL, "--credential", cred}
			}
			command(t, append(base, "send", "--context-id", cid, "--dedupe-key", "k", "--body", "saved")...)
			if got := command(t, append(base, "context-archive", "--context-id", cid)...); got["archived"] != true {
				t.Fatalf("archive output: %v", got)
			}
			if err := commandErr(t, append(base, "send", "--context-id", cid, "--dedupe-key", "new", "--body", "x")...); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("archived send: %v", err)
			}
			ms := commandAny(t, append(base, "replay", "--context-id", cid)...).([]any)
			if len(ms) != 1 {
				t.Fatalf("replay: %+v", ms)
			}
			listed := commandAny(t, append(base, "context-list")...).([]any)
			if len(listed) != 1 || listed[0].(map[string]any)["Archived"] != true || listed[0].(map[string]any)["ArchivedAt"] == nil {
				t.Fatalf("list: %+v", listed)
			}
			if err := commandErr(t, append(base, "context-archive")...); err == nil {
				t.Fatal("missing context ID accepted")
			}
		})
	}
}

func TestServeContextLimitFlags(t *testing.T) {
	listen, depth, count, _, err := serveFlags(nil)
	if err != nil || listen == "" || depth != 3 || count != 20 {
		t.Fatalf("defaults: %q %d %d %v", listen, depth, count, err)
	}
	listen, depth, count, _, err = serveFlags([]string{"--listen", "127.0.0.1:1234", "--max-context-depth", "1", "--max-child-contexts-per-grant", "2"})
	if err != nil || listen != "127.0.0.1:1234" || depth != 1 || count != 2 {
		t.Fatalf("flags: %q %d %d %v", listen, depth, count, err)
	}
	// Exercise the same config path used before serve opens the embedded service.
	api, closeFn, err := open(config{db: filepath.Join(t.TempDir(), "limits.db"), bootstrapCredential: "s", maxContextDepth: depth, maxChildContextsPerGrant: count})
	if err != nil {
		t.Fatal(err)
	}
	defer closeFn()
	ctx := context.Background()
	_, admin, err := api.Bootstrap(ctx, comitia.BootstrapInput{Credential: "s", AdminName: "a", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w, err := api.CreateWorkspace(ctx, admin.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	root, err := api.CreateContext(ctx, admin.Token, comitia.CreateContextInput{WorkspaceID: w.ID, Name: "root"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := api.CreateChildContext(ctx, admin.Token, comitia.CreateChildContextInput{ParentContextID: root.ID, Name: "child"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.CreateChildContext(ctx, admin.Token, comitia.CreateChildContextInput{ParentContextID: child.ID, Name: "too deep"}); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("serve depth: %v", err)
	}
	if _, err := api.CreateChildContext(ctx, admin.Token, comitia.CreateChildContextInput{ParentContextID: root.ID, Name: "second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := api.CreateChildContext(ctx, admin.Token, comitia.CreateChildContextInput{ParentContextID: root.ID, Name: "too many"}); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("serve count: %v", err)
	}
	for _, args := range [][]string{{"--max-context-depth", "0"}, {"--max-context-depth", "-1"}, {"--max-child-contexts-per-grant", "0"}, {"--max-child-contexts-per-grant", "-1"}} {
		if err := run(ctx, append([]string{"--db", filepath.Join(t.TempDir(), "invalid.db"), "serve"}, args...)); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("invalid flags %v: %v", args, err)
		}
	}
}
