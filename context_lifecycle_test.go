package comitia

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func childContext(t *testing.T, f fixture, raw, parent string) WorkContext {
	t.Helper()
	c, err := f.svc.CreateChildContext(context.Background(), raw, CreateChildContextInput{ParentContextID: parent, Name: "child"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestContextDepthLimit(t *testing.T) {
	for _, limit := range []int{0, 1, 5} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			var err error
			f.svc, err = New(Config{Store: f.store, Clock: func() time.Time { return *f.now }, MaxContextDepth: limit})
			if err != nil {
				t.Fatal(err)
			}
			if limit == 0 {
				limit = 3
			}
			parent := f.context
			for depth := 1; depth <= limit; depth++ {
				parent = childContext(t, f, f.admin.Token, parent.ID)
			}
			if _, err := f.svc.CreateChildContext(context.Background(), f.admin.Token, CreateChildContextInput{ParentContextID: parent.ID, Name: "too deep"}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "nesting depth") {
				t.Fatalf("depth error: %v", err)
			}
			if children, err := f.svc.ListChildContexts(context.Background(), f.admin.Token, parent.ID, "", 100); err != nil || len(children) != 0 {
				t.Fatalf("failed creation persisted: %+v %v", children, err)
			}
		})
	}
}

func TestContextCountLimit(t *testing.T) {
	for _, limit := range []int{0, 2} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			var err error
			f.svc, err = New(Config{Store: f.store, Clock: func() time.Time { return *f.now }, MaxChildContextsPerGrant: limit})
			if err != nil {
				t.Fatal(err)
			}
			if limit == 0 {
				limit = 20
			}
			for i := 0; i < limit; i++ {
				c := childContext(t, f, f.admin.Token, f.context.ID)
				if err := f.svc.ArchiveContext(context.Background(), f.admin.Token, c.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.svc.CreateChildContext(context.Background(), f.admin.Token, CreateChildContextInput{ParentContextID: f.context.ID, Name: "too many"}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "per creating grant") {
				t.Fatalf("count error: %v", err)
			}
			// A different creating grant has its own budget on the same parent.
			coord, _ := delegate(t, f, f.admin.Token, "coord", f.context.ID, PermissionCreate|PermissionRead, time.Hour)
			childContext(t, f, coord.Token, f.context.ID)
			children, err := f.svc.ListChildContexts(context.Background(), f.admin.Token, f.context.ID, "", 100)
			if err != nil || len(children) != limit+1 {
				t.Fatalf("children: %d %v", len(children), err)
			}
		})
	}
}

func TestContextCountLimitConcurrent(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	f.svc.maxChildContextsPerGrant = 2
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := f.svc.CreateChildContext(context.Background(), f.admin.Token, CreateChildContextInput{ParentContextID: f.context.ID, Name: "child"})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	created := 0
	for err := range errs {
		if err == nil {
			created++
		} else if !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if created != 2 {
		t.Fatalf("created %d children, want 2", created)
	}
}

func TestContextQuotaBelongsToGrantNotSession(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	f.svc.maxChildContextsPerGrant = 1
	ctx := context.Background()
	_, coord, _, err := f.svc.DelegateSession(ctx, f.admin.Token, DelegateInput{PrincipalName: "coord", TTL: 2 * time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionCreate | PermissionRead, ExpiresAt: f.now.Add(time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	childContext(t, f, coord.Token, f.context.ID)
	if _, err := f.svc.CreateChildContext(ctx, coord.Token, CreateChildContextInput{ParentContextID: f.context.ID, Name: "over first quota"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("first quota: %v", err)
	}
	if _, err := f.svc.GrantContext(ctx, f.admin.Token, GrantInput{SessionID: coord.Session.ID, ContextID: f.context.ID, Permissions: PermissionCreate | PermissionRead, ExpiresAt: f.now.Add(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	childContext(t, f, coord.Token, f.context.ID)
	if _, err := f.svc.CreateChildContext(ctx, coord.Token, CreateChildContextInput{ParentContextID: f.context.ID, Name: "over second quota"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("second quota: %v", err)
	}
}

func TestDeepInheritedReadAfterReducingLimitAndArchive(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	f.svc.maxContextDepth = 65
	ctx := context.Background()
	reader, _ := delegate(t, f, f.admin.Token, "reader", f.context.ID, PermissionRead, time.Hour)
	leaf := f.context
	for depth := 1; depth <= 65; depth++ {
		var err error
		leaf, err = f.svc.CreateChildContext(ctx, f.admin.Token, CreateChildContextInput{ParentContextID: leaf.ID, Name: "child", ParentReadable: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	f.svc.maxContextDepth = 1
	if err := f.svc.ArchiveContext(ctx, f.admin.Token, f.context.ID); err != nil {
		t.Fatal(err)
	}
	// Archiving a parent leaves child grants effective; inherited reads do not depend on creation limits.
	if _, err := f.svc.AppendMessage(ctx, f.admin.Token, leaf.ID, MessageInput{DedupeKey: "k", Body: "saved"}); err != nil {
		t.Fatal(err)
	}
	got, err := f.svc.GetContext(ctx, reader.Token, leaf.ID)
	if err != nil || got.Archived {
		t.Fatalf("deep inherited context: %+v %v", got, err)
	}
	if err := f.svc.ArchiveContext(ctx, f.admin.Token, leaf.ID); err != nil {
		t.Fatal(err)
	}
	if ms, err := f.svc.ReplayMessages(ctx, reader.Token, leaf.ID, 0, 10); err != nil || len(ms) != 1 {
		t.Fatalf("deep archived replay: %+v %v", ms, err)
	}
}

func TestInvalidContextLimits(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	for _, cfg := range []Config{{Store: f.store, MaxContextDepth: -1}, {Store: f.store, MaxChildContextsPerGrant: -1}} {
		if _, err := New(cfg); !errors.Is(err, ErrInvalid) {
			t.Fatalf("config %+v: %v", cfg, err)
		}
	}
}

func TestArchiveAuthorization(t *testing.T) {
	for _, perms := range []Permission{PermissionRead, PermissionWrite, PermissionClaim, PermissionCreate, PermissionAdmin, PermissionGrant} {
		t.Run(fmt.Sprint(perms), func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			member, _ := delegate(t, f, f.admin.Token, "member", f.context.ID, perms, time.Hour)
			err := f.svc.ArchiveContext(context.Background(), member.Token, f.context.ID)
			if perms == PermissionGrant {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrForbidden) {
				t.Fatalf("non-grant archive: %v", err)
			}
		})
	}
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	coord, _ := delegate(t, f, f.admin.Token, "coord", f.context.ID, PermissionCreate|PermissionRead, time.Hour)
	c := childContext(t, f, coord.Token, f.context.ID)
	if err := f.svc.ArchiveContext(ctx, "", c.ID); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("anonymous archive: %v", err)
	}
	if err := f.svc.ArchiveContext(ctx, f.admin.Token, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing archive: %v", err)
	}
	if err := f.svc.ArchiveContext(ctx, f.admin.Token, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.GetContext(ctx, f.admin.Token, c.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("archiving granted admin read: %v", err)
	}
	got, err := f.svc.GetContext(ctx, coord.Token, c.ID)
	if err != nil || !got.Archived || got.ArchivedAt == nil || !got.ArchivedAt.Equal(*f.now) {
		t.Fatalf("archive metadata: %+v %v", got, err)
	}
	*f.now = f.now.Add(time.Minute)
	if err := f.svc.ArchiveContext(ctx, coord.Token, c.ID); err != nil {
		t.Fatal(err)
	}
	again, err := f.svc.GetContext(ctx, coord.Token, c.ID)
	if err != nil || !again.ArchivedAt.Equal(*got.ArchivedAt) {
		t.Fatalf("archive is not idempotent: %+v %v", again, err)
	}
}

// Exercise successful write inputs so archive rejection cannot hide behind input or lease validation.
func assertArchivedWritesAndReads(t *testing.T, f fixture, raw string, c WorkContext, archive func()) {
	t.Helper()
	ctx := context.Background()
	m, err := f.svc.AppendMessage(ctx, raw, c.ID, MessageInput{DedupeKey: "old", Body: "history"})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := f.svc.CreateTask(ctx, raw, c.ID, TaskInput{Title: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	leasing, err := f.svc.CreateTask(ctx, raw, c.ID, TaskInput{Title: "leased"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := f.svc.ClaimTask(ctx, raw, leasing.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	helper, _ := delegate(t, f, raw, "helper", c.ID, PermissionRead, time.Hour)
	archive()
	for name, write := range map[string]func() error{
		"message": func() error {
			_, e := f.svc.AppendMessage(ctx, raw, c.ID, MessageInput{DedupeKey: "new", Body: "x"})
			return e
		},
		"message retry": func() error {
			_, e := f.svc.AppendMessage(ctx, raw, c.ID, MessageInput{DedupeKey: "old", Body: "history"})
			return e
		},
		"task create": func() error { _, e := f.svc.CreateTask(ctx, raw, c.ID, TaskInput{Title: "new"}); return e },
		"task claim":  func() error { _, e := f.svc.ClaimTask(ctx, raw, pending.ID, time.Minute); return e },
		"task renew": func() error {
			_, e := f.svc.RenewClaim(ctx, raw, leasing.ID, claim.Token, claim.Task.LeaseVersion, time.Minute)
			return e
		},
		"task complete": func() error {
			_, e := f.svc.CompleteTask(ctx, raw, leasing.ID, claim.Token, claim.Task.LeaseVersion)
			return e
		},
		"grant": func() error {
			_, e := f.svc.GrantContext(ctx, raw, GrantInput{SessionID: helper.Session.ID, ContextID: c.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Minute)})
			return e
		},
		"admin grant": func() error {
			_, e := f.svc.GrantContext(ctx, f.admin.Token, GrantInput{SessionID: helper.Session.ID, ContextID: c.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Minute)})
			return e
		},
		"delegation": func() error {
			_, _, _, e := f.svc.DelegateSession(ctx, raw, DelegateInput{PrincipalName: "new", TTL: time.Minute, Grants: []DelegatedGrant{{ContextID: c.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Minute)}}})
			return e
		},
		"child": func() error {
			_, e := f.svc.CreateChildContext(ctx, raw, CreateChildContextInput{ParentContextID: c.ID, Name: "new"})
			return e
		},
		"reference": func() error {
			_, e := f.svc.AddReference(ctx, raw, AddReferenceInput{ContextID: c.ID, Kind: "url", URI: "https://example.com"})
			return e
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := write(); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "archived") {
				t.Fatalf("archived write: %v", err)
			}
		})
	}
	if ms, err := f.svc.ReplayMessages(ctx, raw, c.ID, 0, 10); err != nil || len(ms) != 1 || ms[0].ID != m.ID {
		t.Fatalf("replay: %+v %v", ms, err)
	}
	if task, err := f.svc.GetTask(ctx, raw, leasing.ID); err != nil || task.Status != TaskLeased || task.LeaseVersion != claim.Task.LeaseVersion {
		t.Fatalf("get task: %+v %v", task, err)
	}
	if tasks, err := f.svc.ListTasks(ctx, raw, c.ID, "", 10); err != nil || len(tasks) != 2 {
		t.Fatalf("list tasks: %+v %v", tasks, err)
	}
	if err := f.svc.AckMessages(ctx, raw, c.ID, m.Sequence); err != nil {
		t.Fatal(err)
	}
	if cursor, err := f.svc.MessageCursor(ctx, raw, c.ID); err != nil || cursor != m.Sequence {
		t.Fatalf("read cursor: %d %v", cursor, err)
	}
	if listed, err := f.svc.ListContexts(ctx, raw, "", 100); err != nil {
		t.Fatal(err)
	} else {
		found := false
		for _, got := range listed {
			if got.ID == c.ID {
				found = got.Archived && got.ArchivedAt != nil
			}
		}
		if !found {
			t.Fatalf("archived context omitted from listing: %+v", listed)
		}
	}
}

func TestExplicitArchiveWritesAndReads(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	member, _ := delegate(t, f, f.admin.Token, "member", f.context.ID, PermissionAll, 2*time.Hour)
	assertArchivedWritesAndReads(t, f, member.Token, f.context, func() {
		if err := f.svc.ArchiveContext(context.Background(), member.Token, f.context.ID); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAutomaticArchive(t *testing.T) {
	for _, cause := range []string{"creator grant revoked", "parent grant revoked", "creator session revoked", "parent session revoked", "grant expired", "creator session expired", "parent session expired"} {
		t.Run(cause, func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			ctx := context.Background()
			parentTTL, creatorTTL, grantTTL := 3*time.Hour, 2*time.Hour, 2*time.Hour
			if cause == "grant expired" {
				grantTTL = 10 * time.Minute
			}
			if cause == "creator session expired" {
				creatorTTL, grantTTL = 10*time.Minute, 10*time.Minute
			}
			if cause == "parent session expired" {
				parentTTL, creatorTTL, grantTTL = 10*time.Minute, 10*time.Minute, 10*time.Minute
			}
			parent, _ := delegate(t, f, f.admin.Token, "parent", f.context.ID, PermissionAll, parentTTL)
			_, creator, grants, err := f.svc.DelegateSession(ctx, parent.Token, DelegateInput{PrincipalName: "creator", TTL: creatorTTL, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead | PermissionCreate, ExpiresAt: f.now.Add(grantTTL)}}})
			if err != nil {
				t.Fatal(err)
			}
			c := childContext(t, f, creator.Token, f.context.ID)
			// Give the admin an independent grant before archiving, so it survives the creator's chain.
			if _, err := f.svc.GrantContext(ctx, f.admin.Token, GrantInput{SessionID: f.admin.Session.ID, ContextID: c.ID, Permissions: PermissionAll, ExpiresAt: f.now.Add(4 * time.Hour)}); err != nil {
				t.Fatal(err)
			}
			var creatorGrant Grant
			if err := f.store.View(ctx, func(r ReadStore) error {
				gs, err := r.Grants(creator.Session.ID, c.ID, *f.now)
				if err == nil && len(gs) == 1 {
					creatorGrant = gs[0]
				}
				return err
			}); err != nil || creatorGrant.ID == "" {
				t.Fatalf("creator grant: %+v %v", creatorGrant, err)
			}
			// An additional live creator grant must not prevent archival of the recorded creation grant.
			if cause == "creator grant revoked" {
				if _, err := f.svc.GrantContext(ctx, f.admin.Token, GrantInput{SessionID: creator.Session.ID, ContextID: c.ID, Permissions: PermissionAll, ExpiresAt: f.now.Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			endedAt := *f.now
			assertArchivedWritesAndReads(t, f, f.admin.Token, c, func() {
				var err error
				switch cause {
				case "creator grant revoked":
					err = f.svc.RevokeGrant(ctx, f.admin.Token, creatorGrant.ID)
				case "parent grant revoked":
					err = f.svc.RevokeGrant(ctx, f.admin.Token, grants[0].ID)
				case "creator session revoked":
					err = f.svc.RevokeSession(ctx, f.admin.Token, creator.Session.ID)
				case "parent session revoked":
					err = f.svc.RevokeSession(ctx, f.admin.Token, parent.Session.ID)
				default:
					endedAt = f.now.Add(10 * time.Minute)
					*f.now = f.now.Add(10 * time.Minute)
				}
				if err != nil {
					t.Fatal(err)
				}
			})
			got, err := f.svc.GetContext(ctx, f.admin.Token, c.ID)
			if err != nil || !got.Archived || got.ArchivedAt == nil || !got.ArchivedAt.Equal(endedAt) {
				t.Fatalf("auto archive: %+v %v", got, err)
			}
			children, err := f.svc.ListChildContexts(ctx, f.admin.Token, f.context.ID, "", 100)
			if err != nil || len(children) != 1 || !children[0].Archived || !children[0].ArchivedAt.Equal(endedAt) {
				t.Fatalf("children: %+v %v", children, err)
			}
			// Timestamp remains the time access ended, even when observed much later or explicitly archived.
			*f.now = f.now.Add(time.Minute)
			if err := f.svc.ArchiveContext(ctx, f.admin.Token, c.ID); err != nil {
				t.Fatal(err)
			}
			got, err = f.svc.GetContext(ctx, f.admin.Token, c.ID)
			if err != nil || !got.ArchivedAt.Equal(endedAt) {
				t.Fatalf("archive timestamp changed: %+v %v", got, err)
			}
		})
	}
}

func TestContextLifecycleMigrationAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "archive.db")
	f := setup(t, path)
	ctx := context.Background()
	c := childContext(t, f, f.admin.Token, f.context.ID)
	if _, err := f.store.db.Exec("DROP TABLE context_creators; DROP TABLE context_archives"); err != nil {
		t.Fatal(err)
	}
	f.store.Close()
	st, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := New(Config{Store: st, Clock: func() time.Time { return *f.now }, MaxChildContextsPerGrant: 1})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateChildContext(ctx, f.admin.Token, CreateChildContextInput{ParentContextID: f.context.ID, Name: "over migrated quota"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("backfill quota: %v", err)
	}
	if err := svc.ArchiveContext(ctx, f.admin.Token, c.ID); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err = New(Config{Store: st, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetContext(ctx, f.admin.Token, c.ID)
	if err != nil || !got.Archived || got.ArchivedAt == nil || !got.ArchivedAt.Equal(*f.now) {
		t.Fatalf("persisted archive: %+v %v", got, err)
	}
	root, err := svc.GetContext(ctx, f.admin.Token, f.context.ID)
	if err != nil || root.Archived || root.ArchivedAt != nil {
		t.Fatalf("migrated root: %+v %v", root, err)
	}
}
