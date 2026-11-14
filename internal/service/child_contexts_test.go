package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"

	"github.com/krimvp/comitia/internal/sqlstore"
)

// delegate mints a child session of parent holding perms on cid.
func delegate(t *testing.T, f fixture, parent, name, cid string, perms model.Permission, ttl time.Duration) (model.Credential, model.Grant) {
	t.Helper()
	_, c, gs, err := f.svc.DelegateSession(context.Background(), parent, model.DelegateInput{
		PrincipalName: name, TTL: ttl,
		Grants: []model.DelegatedGrant{{ContextID: cid, Permissions: perms, ExpiresAt: f.now.Add(ttl)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, gs[0]
}

func TestCreateChildContext(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "task 15"})
	if err != nil {
		t.Fatal(err)
	}
	coord, parentGrant := delegate(t, f, f.admin.Token, "coord", f.context.ID, model.PermissionRead|model.PermissionWrite|model.PermissionClaim|model.PermissionCreate, 2*time.Hour)
	child, err := f.svc.CreateChildContext(ctx, coord.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "task-15", TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentContextID != f.context.ID || child.TaskID != task.ID || child.WorkspaceID != f.context.WorkspaceID || child.CreatorSessionID != coord.Session.ID || child.ParentReadable {
		t.Fatalf("child: %+v", child)
	}
	if _, err := f.svc.AppendMessage(ctx, coord.Token, child.ID, model.MessageInput{DedupeKey: "k", Body: "hello"}); err != nil {
		t.Fatal(err)
	}
	if ms, err := f.svc.ReplayMessages(ctx, coord.Token, child.ID, 0, 10); err != nil || len(ms) != 1 {
		t.Fatalf("replay: %+v %v", ms, err)
	}
	if _, err := f.svc.CreateTask(ctx, coord.Token, child.ID, model.TaskInput{Title: "sub"}); err != nil {
		t.Fatal(err)
	}
	var grants []model.Grant
	if err := f.store.View(ctx, func(r model.ReadStore) (e error) {
		grants, e = r.Grants(coord.Session.ID, child.ID, f.now.UTC())
		return e
	}); err != nil || len(grants) != 1 {
		t.Fatalf("grants: %+v %v", grants, err)
	}
	if g := grants[0]; g.ParentGrantID != parentGrant.ID || g.Permissions != childPermissions || !g.ExpiresAt.Equal(parentGrant.ExpiresAt) {
		t.Fatalf("child grant: %+v (parent %+v)", g, parentGrant)
	}
	got, err := f.svc.GetContext(ctx, coord.Token, child.ID)
	if err != nil || got != child {
		t.Fatalf("get: %+v %v", got, err)
	}
	listed, err := f.svc.ListContexts(ctx, coord.Token, "", 100)
	if err != nil || len(listed) != 2 {
		t.Fatalf("list: %+v %v", listed, err)
	}
	for _, c := range listed {
		if c.ID == child.ID && (c.ParentContextID != f.context.ID || c.TaskID != task.ID) {
			t.Fatalf("listed child: %+v", c)
		}
		if c.ID == f.context.ID && (c.ParentContextID != "" || c.TaskID != "") {
			t.Fatalf("listed parent: %+v", c)
		}
	}
	// The creator holds create on the child, so it can nest further.
	if _, err := f.svc.CreateChildContext(ctx, coord.Token, model.CreateChildContextInput{ParentContextID: child.ID, Name: "nested"}); err != nil {
		t.Fatal(err)
	}
}

func TestCreateChildContextRefusals(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	writer, _ := delegate(t, f, f.admin.Token, "writer", f.context.ID, model.PermissionRead|model.PermissionWrite|model.PermissionClaim|model.PermissionGrant, time.Hour)
	if _, err := f.svc.CreateChildContext(ctx, writer.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "x"}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("without create: %v", err)
	}
	// Administrative status alone does not grant create on a context.
	admin, err := f.svc.MintSession(ctx, f.admin.Token, f.admin.Session.PrincipalID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateChildContext(ctx, admin.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "x"}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("admin without grant: %v", err)
	}
	if _, err := f.svc.CreateChildContext(ctx, f.admin.Token, model.CreateChildContextInput{ParentContextID: "missing", Name: "x"}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("missing parent: %v", err)
	}
	for _, in := range []model.CreateChildContextInput{{ParentContextID: f.context.ID}, {Name: "x"}} {
		if _, err := f.svc.CreateChildContext(ctx, f.admin.Token, in); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	// TaskID must be a task of the parent, never of a sibling context or another workspace.
	sibling, err := f.svc.CreateContext(ctx, f.admin.Token, model.CreateContextInput{WorkspaceID: f.context.WorkspaceID, Name: "sibling"})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := f.svc.CreateWorkspace(ctx, f.admin.Token, "other")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := f.svc.CreateContext(ctx, f.admin.Token, model.CreateContextInput{WorkspaceID: ws.ID, Name: "foreign"})
	if err != nil {
		t.Fatal(err)
	}
	for _, cid := range []string{sibling.ID, foreign.ID} {
		task, err := f.svc.CreateTask(ctx, f.admin.Token, cid, model.TaskInput{Title: "t"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.CreateChildContext(ctx, f.admin.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "x", TaskID: task.ID}); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("task of %s: %v", cid, err)
		}
	}
	if _, err := f.svc.CreateChildContext(ctx, f.admin.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "x", TaskID: "missing"}); !errors.Is(err, model.ErrInvalid) {
		t.Fatalf("missing task: %v", err)
	}
	// The child always lands in the parent's workspace.
	child, err := f.svc.CreateChildContext(ctx, f.admin.Token, model.CreateChildContextInput{ParentContextID: foreign.ID, Name: "x"})
	if err != nil || child.WorkspaceID != ws.ID {
		t.Fatalf("workspace: %+v %v", child, err)
	}
}

func TestChildContextPrivacy(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	coord, _ := delegate(t, f, f.admin.Token, "coord", f.context.ID, model.PermissionRead|model.PermissionWrite|model.PermissionCreate, time.Hour)
	member, _ := delegate(t, f, f.admin.Token, "member", f.context.ID, model.PermissionRead|model.PermissionWrite|model.PermissionClaim|model.PermissionGrant, time.Hour)
	private, err := f.svc.CreateChildContext(ctx, coord.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	audited, err := f.svc.CreateChildContext(ctx, coord.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "audited", ParentReadable: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []model.WorkContext{private, audited} {
		if _, err := f.svc.AppendMessage(ctx, coord.Token, c.ID, model.MessageInput{DedupeKey: "k", Body: "internal"}); err != nil {
			t.Fatal(err)
		}
	}
	// Parent members see metadata of every child, but not their contents.
	children, err := f.svc.ListChildContexts(ctx, member.Token, f.context.ID, "", 100)
	if err != nil || len(children) != 2 {
		t.Fatalf("children: %+v %v", children, err)
	}
	for _, c := range children {
		if c.ParentContextID != f.context.ID || c.CreatorSessionID != coord.Session.ID || c.ParentReadable != (c.ID == audited.ID) {
			t.Fatalf("metadata: %+v", c)
		}
	}
	page, err := f.svc.ListChildContexts(ctx, member.Token, f.context.ID, children[0].ID, 1)
	if err != nil || len(page) != 1 || page[0].ID != children[1].ID {
		t.Fatalf("page: %+v %v", page, err)
	}
	if _, err := f.svc.ReplayMessages(ctx, member.Token, private.ID, 0, 10); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("private replay: %v", err)
	}
	if _, err := f.svc.ListTasks(ctx, member.Token, private.ID, "", 10); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("private tasks: %v", err)
	}
	if _, err := f.svc.GetContext(ctx, member.Token, private.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("private get: %v", err)
	}
	if listed, err := f.svc.ListContexts(ctx, member.Token, "", 100); err != nil || len(listed) != 1 {
		t.Fatalf("member list: %+v %v", listed, err)
	}
	// parent_readable grants read only: no write, task creation, claims, delegation or nesting.
	if ms, err := f.svc.ReplayMessages(ctx, member.Token, audited.ID, 0, 10); err != nil || len(ms) != 1 {
		t.Fatalf("audited replay: %+v %v", ms, err)
	}
	if _, err := f.svc.AppendMessage(ctx, member.Token, audited.ID, model.MessageInput{DedupeKey: "m", Body: "x"}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("audited write: %v", err)
	}
	if _, err := f.svc.CreateTask(ctx, member.Token, audited.ID, model.TaskInput{Title: "x"}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("audited task: %v", err)
	}
	task, err := f.svc.CreateTask(ctx, coord.Token, audited.ID, model.TaskInput{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ClaimTask(ctx, member.Token, task.ID, time.Minute); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("audited claim: %v", err)
	}
	if _, _, _, err := f.svc.DelegateSession(ctx, member.Token, model.DelegateInput{PrincipalName: "x", TTL: time.Minute, Grants: []model.DelegatedGrant{{ContextID: audited.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(time.Minute)}}}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("audited delegate: %v", err)
	}
	if _, err := f.svc.CreateChildContext(ctx, member.Token, model.CreateChildContextInput{ParentContextID: audited.ID, Name: "x"}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("audited nest: %v", err)
	}
	// Outsiders of the parent see nothing.
	outsider, _ := delegate(t, f, coord.Token, "outsider", private.ID, model.PermissionRead, 30*time.Minute)
	if _, err := f.svc.ListChildContexts(ctx, outsider.Token, f.context.ID, "", 10); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("outsider children: %v", err)
	}
	if _, err := f.svc.ReplayMessages(ctx, outsider.Token, audited.ID, 0, 10); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("outsider audited: %v", err)
	}
	for _, limit := range []int{0, 1001} {
		if _, err := f.svc.ListChildContexts(ctx, member.Token, f.context.ID, "", limit); !errors.Is(err, model.ErrInvalid) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
}

func TestRevokingParentGrantCascadesToChild(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	coord, parentGrant := delegate(t, f, f.admin.Token, "coord", f.context.ID, model.PermissionRead|model.PermissionCreate, time.Hour)
	child, err := f.svc.CreateChildContext(ctx, coord.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "task-15"})
	if err != nil {
		t.Fatal(err)
	}
	helper, _ := delegate(t, f, coord.Token, "helper", child.ID, model.PermissionRead|model.PermissionWrite, 30*time.Minute)
	if _, err := f.svc.AppendMessage(ctx, helper.Token, child.ID, model.MessageInput{DedupeKey: "h", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.RevokeGrant(ctx, f.admin.Token, parentGrant.ID); err != nil {
		t.Fatal(err)
	}
	for name, tok := range map[string]string{"coord": coord.Token, "helper": helper.Token} {
		if _, err := f.svc.ReplayMessages(ctx, tok, child.ID, 0, 10); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("%s after revoke: %v", name, err)
		}
	}
	if _, err := f.svc.AppendMessage(ctx, helper.Token, child.ID, model.MessageInput{DedupeKey: "h2", Body: "x"}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("helper write after revoke: %v", err)
	}
	if listed, err := f.svc.ListContexts(ctx, helper.Token, "", 10); err != nil || len(listed) != 0 {
		t.Fatalf("helper list: %+v %v", listed, err)
	}
}

func TestChildContextGrantExpiresWithParentGrant(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	_, coord, _, err := f.svc.DelegateSession(ctx, f.admin.Token, model.DelegateInput{
		PrincipalName: "coord", TTL: time.Hour,
		Grants: []model.DelegatedGrant{{ContextID: f.context.ID, Permissions: model.PermissionRead | model.PermissionCreate, ExpiresAt: f.now.Add(10 * time.Minute)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := f.svc.CreateChildContext(ctx, coord.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(11 * time.Minute)
	if _, err := f.svc.ReplayMessages(ctx, coord.Token, child.ID, 0, 10); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("after parent grant expiry: %v", err)
	}
	// The admin that created the parent holds no grant on the private child.
	if _, err := f.svc.ReplayMessages(ctx, f.admin.Token, child.ID, 0, 10); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("admin replay of private child: %v", err)
	}
}

func TestContextParentsMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	f := setup(t, path)
	if _, err := sqlstore.DB(f.store).Exec("DROP TABLE context_parents"); err != nil {
		t.Fatal(err)
	}
	f.store.Close()
	st, err := sqlstore.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := New(model.Config{Store: st, Clock: func() time.Time { return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetContext(context.Background(), f.admin.Token, f.context.ID)
	if err != nil || got.ParentContextID != "" || got.TaskID != "" || got.ParentReadable {
		t.Fatalf("existing context: %+v %v", got, err)
	}
}

func TestParentReadableChains(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	coord, _ := delegate(t, f, f.admin.Token, "coord", f.context.ID, model.PermissionRead|model.PermissionCreate, time.Hour)
	member, _ := delegate(t, f, f.admin.Token, "member", f.context.ID, model.PermissionRead, time.Hour)
	create := func(parent string, readable bool) model.WorkContext {
		t.Helper()
		c, err := f.svc.CreateChildContext(ctx, coord.Token, model.CreateChildContextInput{ParentContextID: parent, Name: "c", ParentReadable: readable})
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	underReadable := create(create(f.context.ID, true).ID, true)
	underPrivate := create(create(f.context.ID, false).ID, true)
	if _, err := f.svc.ReplayMessages(ctx, member.Token, underReadable.ID, 0, 10); err != nil {
		t.Fatalf("readable chain: %v", err)
	}
	if _, err := f.svc.ReplayMessages(ctx, member.Token, underPrivate.ID, 0, 10); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("chain through private child: %v", err)
	}
}

func TestRevokingCreatorSessionCascadesToChild(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	coord, _ := delegate(t, f, f.admin.Token, "coord", f.context.ID, model.PermissionRead|model.PermissionCreate, time.Hour)
	child, err := f.svc.CreateChildContext(ctx, coord.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	helper, _ := delegate(t, f, coord.Token, "helper", child.ID, model.PermissionRead|model.PermissionWrite, 30*time.Minute)
	if err := f.svc.RevokeSession(ctx, f.admin.Token, coord.Session.ID); err != nil {
		t.Fatal(err)
	}
	for name, tok := range map[string]string{"coord": coord.Token, "helper": helper.Token} {
		if _, err := f.svc.ReplayMessages(ctx, tok, child.ID, 0, 10); !errors.Is(err, model.ErrUnauthenticated) {
			t.Fatalf("%s after session revoke: %v", name, err)
		}
	}
}

// Read through a parent_readable child allows a session's own cursor, but it is not a direct grant, so the session
// cannot be addressed as a recipient in the child.
func TestParentReadableCursorButNotRecipient(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	coord, _ := delegate(t, f, f.admin.Token, "coord", f.context.ID, model.PermissionRead|model.PermissionCreate, time.Hour)
	member, _ := delegate(t, f, f.admin.Token, "member", f.context.ID, model.PermissionRead, time.Hour)
	child, err := f.svc.CreateChildContext(ctx, coord.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "c", ParentReadable: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AppendMessage(ctx, coord.Token, child.ID, model.MessageInput{DedupeKey: "k", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.AckMessages(ctx, member.Token, child.ID, 1); err != nil {
		t.Fatal(err)
	}
	if cur, err := f.svc.MessageCursor(ctx, member.Token, child.ID); err != nil || cur != 1 {
		t.Fatalf("cursor: %d %v", cur, err)
	}
	direct := &model.MessageEnvelope{Schema: "comitia.discussion.v1", Payload: []byte(`{}`), RecipientSessionIDs: []string{coord.Session.ID}}
	if _, err := f.svc.AppendMessage(ctx, coord.Token, child.ID, model.MessageInput{DedupeKey: "d", Body: "x", Envelope: direct}); err != nil {
		t.Fatalf("direct recipient: %v", err)
	}
	env := &model.MessageEnvelope{Schema: "comitia.discussion.v1", Payload: []byte(`{}`), RecipientSessionIDs: []string{member.Session.ID}}
	if _, err := f.svc.AppendMessage(ctx, coord.Token, child.ID, model.MessageInput{DedupeKey: "r", Body: "x", Envelope: env}); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("recipient via parent read: %v", err)
	}
}
