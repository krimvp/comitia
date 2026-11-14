package comitia

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/sqlstore"
)

func TestTaskDiscoveryTieOrder(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	err := f.store.Update(context.Background(), func(w WriteStore) error {
		for i := 1001; i >= 0; i-- {
			if err := w.PutTask(Task{ID: fmt.Sprintf("task-%04d", i), ContextID: f.context.ID, Title: "t", Status: TaskPending, CreatedAt: *f.now, UpdatedAt: *f.now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := f.svc.ListTasks(context.Background(), f.admin.Token, f.context.ID, "", 1000)
	if err != nil || len(tasks) != 1000 || tasks[0].ID != "task-0000" || tasks[999].ID != "task-0999" {
		t.Fatalf("unstable tied task order: first=%s last=%s err=%v", tasks[0].ID, tasks[len(tasks)-1].ID, err)
	}
}

func TestParticipantDiscoveryIndex(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	rows, err := sqlstore.DB(f.store).Query("EXPLAIN QUERY PLAN SELECT DISTINCT session_id FROM grants WHERE context_id=? AND session_id>? ORDER BY session_id", f.context.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var a, b, c int
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "grants_context_sessions") || strings.Contains(plan, "USE TEMP B-TREE") {
		t.Fatalf("participant lookup lacks covering context-first index: %s", plan)
	}
}

func TestDiscoveryPagesBeyondThousand(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	err := f.store.Update(ctx, func(w WriteStore) error {
		for i := 1004; i >= 0; i-- {
			tid := fmt.Sprintf("task-%04d", i)
			status := TaskPending
			if i%2 == 1 {
				status = TaskCompleted
			}
			if err := w.PutTask(Task{ID: tid, ContextID: f.context.ID, Title: tid, Status: status, CreatedAt: *f.now, UpdatedAt: *f.now, LeaseTokenHash: "secret"}); err != nil {
				return err
			}
			sid := fmt.Sprintf("session-%04d", i)
			exp := f.now.Add(time.Hour)
			var rev *time.Time
			if i%7 == 0 {
				rev = f.now
			}
			if i%7 == 1 {
				exp = *f.now
			}
			if err := w.PutSession(Session{ID: sid, PrincipalID: f.admin.Session.PrincipalID, ExpiresAt: exp, RevokedAt: rev, CreatedAt: *f.now}, tokenHash(sid)); err != nil {
				return err
			}
			perm := PermissionRead
			if i%7 == 2 {
				perm = PermissionWrite
			}
			g := Grant{ID: "grant-" + sid, SessionID: sid, ContextID: f.context.ID, IssuerSessionID: f.admin.Session.ID, Permissions: perm, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}
			if i%7 == 3 {
				g.RevokedAt = f.now
			}
			if i%7 == 4 {
				g.ExpiresAt = *f.now
			}
			if err := w.PutGrant(g); err != nil {
				return err
			}
			g.ID += "-duplicate"
			if err := w.PutGrant(g); err != nil {
				return err
			}
		}
		// More than 1,000 readable participants, interspersed with hidden candidates.
		for i := 0; i < 1005; i++ {
			sid := fmt.Sprintf("tail-%04d", i)
			if err := w.PutSession(Session{ID: sid, PrincipalID: f.admin.Session.PrincipalID, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}, tokenHash(sid)); err != nil {
				return err
			}
			if err := w.PutGrant(Grant{ID: "grant-" + sid, SessionID: sid, ContextID: f.context.ID, IssuerSessionID: f.admin.Session.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 99, 1000} {
		after := ""
		seen := map[string]bool{}
		for {
			p, err := f.svc.ListTasksPage(ctx, f.admin.Token, f.context.ID, "", after, limit)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Tasks) > limit {
				t.Fatal("limit exceeded")
			}
			for _, task := range p.Tasks {
				if seen[task.ID] || task.LeaseTokenHash != "" {
					t.Fatalf("duplicate/leak: %+v", task)
				}
				seen[task.ID] = true
			}
			if !p.HasMore {
				break
			}
			if p.NextAfter == after || len(p.Tasks) == 0 {
				t.Fatal("cursor did not advance")
			}
			after = p.NextAfter
		}
		if len(seen) != 1005 {
			t.Fatalf("task tail unreachable: %d", len(seen))
		}
	}
	p, err := f.svc.ListTasksPage(ctx, f.admin.Token, f.context.ID, TaskPending, "", 1000)
	if err != nil || len(p.Tasks) != 503 || p.HasMore {
		t.Fatalf("status page: %d %t %v", len(p.Tasks), p.HasMore, err)
	}
	for _, limit := range []int{1, 100, 1000} {
		after := ""
		seen := map[string]bool{}
		for {
			p, err := f.svc.ListParticipantsPage(ctx, f.admin.Token, f.context.ID, after, limit)
			if err != nil {
				t.Fatal(err)
			}
			for _, participant := range p.Participants {
				sid := participant.SessionID
				if seen[sid] || sid <= after {
					t.Fatalf("duplicate/nonexclusive: %s", sid)
				}
				if strings.HasPrefix(sid, "session-") {
					var i int
					fmt.Sscanf(sid, "session-%d", &i)
					if i%7 < 5 {
						t.Fatalf("hidden session leaked: %s", sid)
					}
				}
				seen[sid] = true
			}
			if !p.HasMore {
				break
			}
			if p.NextAfter == after || len(p.Participants) == 0 {
				t.Fatal("participant cursor did not advance")
			}
			after = p.NextAfter
		}
		if len(seen) != 1292 {
			t.Fatalf("participant tail or filter wrong: %d", len(seen))
		}
	}
}

func TestDiscoveryCursorValidationAndDeletion(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	a, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "first"})
	if err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(time.Second)
	b, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "second"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.ListTasksPage(ctx, f.admin.Token, f.context.ID, "", "", 1)
	if err != nil || !p.HasMore || p.Tasks[0].ID != a.ID {
		t.Fatalf("first page: %+v %v", p, err)
	}
	if _, err = sqlstore.DB(f.store).Exec("DELETE FROM tasks WHERE id=?", a.ID); err != nil {
		t.Fatal(err)
	}
	p, err = f.svc.ListTasksPage(ctx, f.admin.Token, f.context.ID, "", p.NextAfter, 1)
	if err != nil || p.HasMore || len(p.Tasks) != 1 || p.Tasks[0].ID != b.ID {
		t.Fatalf("deleted anchor: %+v %v", p, err)
	}
	empty, err := f.svc.ListTasksPage(ctx, f.admin.Token, f.context.ID, "", p.NextAfter, 1)
	if err != nil || len(empty.Tasks) != 0 || empty.Tasks == nil || empty.HasMore || empty.NextAfter != p.NextAfter {
		t.Fatalf("empty page: %+v %v", empty, err)
	}
	for _, after := range []string{"bad", base64.RawURLEncoding.EncodeToString([]byte(`{"id":"x","created_at":"bad"}`)), strings.Repeat("a", 1000)} {
		if _, err := f.svc.ListTasksPage(ctx, f.admin.Token, f.context.ID, "", after, 1); !errors.Is(err, ErrInvalid) {
			t.Fatalf("cursor=%q: %v", after, err)
		}
	}
	for _, limit := range []int{0, 1001} {
		if _, err := f.svc.ListTasksPage(ctx, f.admin.Token, f.context.ID, "", "", limit); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := f.svc.ListParticipantsPage(ctx, f.admin.Token, f.context.ID, "", limit); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.ListTasksPage(ctx, f.admin.Token, f.context.ID, "bogus", "", 1); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := f.svc.ListTasksPage(ctx, "bad", f.context.ID, "", "", 1); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal(err)
	}
	if _, err := f.svc.ListParticipantsPage(ctx, "bad", f.context.ID, "", 1); !errors.Is(err, ErrUnauthenticated) {
		t.Fatal(err)
	}
}

func TestDiscoveryIndexesMigrateExistingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "discovery.db")
	f := setup(t, path)
	for _, index := range []string{"grants_context_sessions", "tasks_page", "tasks_status_page"} {
		if _, err := sqlstore.DB(f.store).Exec("DROP INDEX " + index); err != nil {
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
	for _, index := range []string{"grants_context_sessions", "tasks_page", "tasks_status_page"} {
		var n int
		if err := sqlstore.DB(st).QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name=?", index).Scan(&n); err != nil || n != 1 {
			t.Fatalf("migration index=%s count=%d err=%v", index, n, err)
		}
	}
}
