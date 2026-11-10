package main

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/krimvp/comitia"
)

func TestReferenceListReadsBackAddedReferences(t *testing.T) {
	db := filepath.Join(t.TempDir(), "references.db")
	boot := command(t, "--db", db, "--bootstrap-credential", "once", "bootstrap")
	base := []string{"--db", db, "--credential", boot["credential"].(map[string]any)["Token"].(string)}
	w := command(t, append(base, "workspace-create", "--name", "w")...)
	c := command(t, append(base, "context-create", "--workspace-id", w["ID"].(string), "--name", "c")...)
	cid := c["ID"].(string)
	for _, uri := range []string{"https://example.test/1", "https://example.test/2"} {
		command(t, append(base, "reference-add", "--context-id", cid, "--kind", "issue", "--uri", uri)...)
	}
	if err := commandErr(t, append(base, "reference-add", "--context-id", cid, "--kind", "issue", "--uri", "not a uri")...); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("unschemed uri: %v", err)
	}
	page := command(t, append(base, "reference-list", "--context-id", cid, "--limit", "1")...)
	refs := page["references"].([]any)
	if len(refs) != 1 || page["has_more"] != true || page["next_after"] == "" {
		t.Fatalf("first page: %v", page)
	}
	rest := command(t, append(base, "reference-list", "--context-id", cid, "--after-id", page["next_after"].(string))...)
	if len(rest["references"].([]any)) != 1 || rest["has_more"] != false {
		t.Fatalf("rest: %v", rest)
	}
	if err := commandErr(t, append(base, "reference-list")...); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("missing context: %v", err)
	}
}
