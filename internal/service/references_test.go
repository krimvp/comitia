package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

func TestListReferencesPagesInCreationOrderWithReadAccess(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	var want []model.Reference
	for i, uri := range []string{"https://example.test/1", "urn:isbn:0451450523", "file:///tmp/notes.md"} {
		*f.now = f.now.Add(time.Duration(i%2) * time.Second) // Two share a timestamp: ID breaks the tie.
		ref, err := f.svc.AddReference(ctx, f.admin.Token, model.AddReferenceInput{ContextID: f.context.ID, Kind: "doc", URI: uri})
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, ref)
	}
	if want[1].CreatedAt.Equal(want[2].CreatedAt) && want[2].ID < want[1].ID {
		want[1], want[2] = want[2], want[1]
	}
	reader, _ := delegate(t, f, f.admin.Token, "reader", f.context.ID, model.PermissionRead, time.Hour)
	var got []model.Reference
	after := ""
	for pages := 0; ; pages++ {
		page, err := ListReferences(ctx, f.svc, reader.Token, f.context.ID, after, 2)
		if err != nil || pages > 2 {
			t.Fatalf("page %d: %+v %v", pages, page, err)
		}
		got = append(got, page.References...)
		after = page.NextAfter
		if !page.HasMore {
			break
		}
	}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("got %+v want %+v", got, want)
	}
	if page, err := f.svc.ListReferences(ctx, reader.Token, f.context.ID, after, 2); err != nil || len(page.References) != 0 || page.HasMore || page.NextAfter != after {
		t.Fatalf("end: %+v %v", page, err)
	}
	// Archived contexts stay readable; listing needs read and a valid page.
	if err := f.svc.ArchiveContext(ctx, f.admin.Token, f.context.ID); err != nil {
		t.Fatal(err)
	}
	if page, err := f.svc.ListReferences(ctx, reader.Token, f.context.ID, "", 10); err != nil || len(page.References) != 3 {
		t.Fatalf("archived: %+v %v", page, err)
	}
	other, err := f.svc.CreateContext(ctx, f.admin.Token, model.CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	for _, cid := range []string{other.ID, "missing"} {
		if _, err := f.svc.ListReferences(ctx, reader.Token, cid, "", 10); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("%s: %v", cid, err)
		}
	}
	if _, err := f.svc.ListReferences(ctx, "invalid", f.context.ID, "", 10); !errors.Is(err, model.ErrUnauthenticated) {
		t.Fatalf("unauthenticated: %v", err)
	}
	for _, tc := range []struct {
		after string
		limit int
	}{{"", 0}, {"", 1001}, {"not-a-cursor", 10}} {
		if _, err := f.svc.ListReferences(ctx, reader.Token, f.context.ID, tc.after, tc.limit); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}

func TestListReferencesFollowsParentReadableInheritance(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	coord, _ := delegate(t, f, f.admin.Token, "coord", f.context.ID, model.PermissionRead|model.PermissionWrite|model.PermissionCreate, time.Hour)
	member, _ := delegate(t, f, f.admin.Token, "member", f.context.ID, model.PermissionRead, time.Hour)
	for _, readable := range []bool{false, true} {
		child, err := f.svc.CreateChildContext(ctx, coord.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "child", ParentReadable: readable})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.AddReference(ctx, coord.Token, model.AddReferenceInput{ContextID: child.ID, Kind: "issue", URI: "https://example.test/7"}); err != nil {
			t.Fatal(err)
		}
		page, err := f.svc.ListReferences(ctx, member.Token, child.ID, "", 10)
		if readable && (err != nil || len(page.References) != 1) || !readable && !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("parent_readable=%t: %+v %v", readable, page, err)
		}
	}
}

func TestListReferencesStopsAtPageByteBudget(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	uri := "https://example.test/" + strings.Repeat("u", model.MaxReferenceURIBytes-len("https://example.test/"))
	for i := 0; i < model.ListPageBytes/model.MaxReferenceURIBytes+5; i++ {
		if _, err := f.svc.AddReference(ctx, f.admin.Token, model.AddReferenceInput{ContextID: f.context.ID, Kind: "doc", URI: uri}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.svc.ListReferences(ctx, f.admin.Token, f.context.ID, "", 1000)
	if err != nil || !page.HasMore || len(page.References) == 0 || len(page.References) >= model.ListPageBytes/model.MaxReferenceURIBytes+5 {
		t.Fatalf("page: %d %t %v", len(page.References), page.HasMore, err)
	}
	if n, _ := boundedCount(page.References, model.ListPageBytes); n != len(page.References) {
		t.Fatalf("page exceeds budget")
	}
	next, err := f.svc.ListReferences(ctx, f.admin.Token, f.context.ID, page.NextAfter, 1000)
	if err != nil || len(next.References) == 0 || next.References[0].ID == page.References[len(page.References)-1].ID {
		t.Fatalf("next: %d %v", len(next.References), err)
	}
}

func TestAddReferenceRequiresTokenKindAndSchemedURI(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	for _, tc := range []struct{ kind, uri string }{
		{"doc", "not a uri at all"}, {"doc", "/relative/path"}, {"doc", "example.test/x"}, {"doc", "https://exa mple.test"}, {"doc", "https://example.test/\x00"},
		{"has space", "https://example.test"}, {"-lead", "https://example.test"}, {"<b>", "https://example.test"}, {"kind\n", "https://example.test"},
	} {
		if err := f.addReference(tc.kind, tc.uri); !errors.Is(err, model.ErrInvalid) {
			t.Errorf("%q %q: %v", tc.kind, tc.uri, err)
		}
	}
	for _, kind := range []string{"issue", "github/pr", "doc.v2", "RFC_9110", "a+b-c"} {
		if err := f.addReference(kind, "mailto:team@example.test"); err != nil {
			t.Errorf("%q: %v", kind, err)
		}
	}
}

// Rows stored before validation stayed unchecked; listing returns them as stored.
func TestListReferencesReturnsLegacyRowsUnvalidated(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	legacy := model.Reference{ID: "legacy", ContextID: f.context.ID, Kind: "free form", URI: "not a uri", CreatedAt: *f.now}
	if err := f.store.Update(ctx, func(w model.WriteStore) error { return w.PutReference(legacy) }); err != nil {
		t.Fatal(err)
	}
	page, err := f.svc.ListReferences(ctx, f.admin.Token, f.context.ID, "", 10)
	if err != nil || len(page.References) != 1 || page.References[0] != legacy {
		t.Fatalf("%+v %v", page, err)
	}
}

func TestListReferencesIsOptionalForAPIsAndStores(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	if _, err := ListReferences(ctx, struct{ model.ContextAPI }{f.svc}, f.admin.Token, f.context.ID, "", 10); !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("api: %v", err)
	}
	svc, err := New(model.Config{Store: plainStore{f.store}, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ListReferences(ctx, f.admin.Token, f.context.ID, "", 10); !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("store: %v", err)
	}
}

// plainStore hides optional store capabilities from the Service.
type plainStore struct{ s model.Store }

func (p plainStore) View(ctx context.Context, f func(model.ReadStore) error) error {
	return p.s.View(ctx, func(r model.ReadStore) error { return f(struct{ model.ReadStore }{r}) })
}
func (p plainStore) Update(ctx context.Context, f func(model.WriteStore) error) error {
	return p.s.Update(ctx, func(w model.WriteStore) error { return f(struct{ model.WriteStore }{w}) })
}
func (p plainStore) Close() error { return nil }
