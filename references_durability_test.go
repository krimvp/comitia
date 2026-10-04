package comitia

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestReferenceFieldsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "references.db")
	f := setup(t, path)
	input := AddReferenceInput{ContextID: f.context.ID, Kind: "issue", URI: "https://example.test/issues/55"}
	reference, err := f.svc.AddReference(context.Background(), f.admin.Token, input)
	if err != nil || reference.ID == "" || reference.ContextID != input.ContextID || reference.Kind != input.Kind || reference.URI != input.URI || !reference.CreatedAt.Equal(*f.now) {
		t.Fatalf("add: %+v %v", reference, err)
	}
	invalid := input
	invalid.URI = ""
	if _, err := f.svc.AddReference(context.Background(), f.admin.Token, invalid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}
	if _, err := f.svc.AddReference(context.Background(), "invalid", input); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("unauthenticated: %v", err)
	}
	invalid = input
	invalid.Kind = ""
	if _, err := f.svc.AddReference(context.Background(), f.admin.Token, invalid); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing kind: %v", err)
	}
	if err := f.store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var got Reference
	var created string
	if err := reopened.db.QueryRow("SELECT id,context_id,kind,uri,created_at FROM refs WHERE id=?", reference.ID).Scan(&got.ID, &got.ContextID, &got.Kind, &got.URI, &created); err != nil {
		t.Fatal(err)
	}
	got.CreatedAt = parse(created)
	if got != reference {
		t.Fatalf("persisted: %+v want %+v", got, reference)
	}
	var count int
	if err := reopened.db.QueryRow("SELECT COUNT(*) FROM refs").Scan(&count); err != nil || count != 1 {
		t.Fatalf("invalid writes persisted: %d %v", count, err)
	}
}
