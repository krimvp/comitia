package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func encodedLen(t *testing.T, v any) int {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return len(raw)
}

func TestTaskPagesStopAtByteBudget(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	tok, cid := f.admin.Token, f.context.ID
	legacy := Task{ID: "a-legacy", ContextID: cid, Title: "legacy", Body: strings.Repeat("x", 2*ListPageBytes), Status: TaskPending, CreatedAt: *f.now, UpdatedAt: *f.now}
	if err := f.store.Update(ctx, func(w WriteStore) error { return w.PutTask(legacy) }); err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(time.Second)
	for i := 0; i < 5; i++ {
		if err := f.createTask(TaskInput{Title: fmt.Sprint(i), Body: strings.Repeat("b", 400<<10)}); err != nil {
			t.Fatal(err)
		}
	}
	read := map[string]func(after string) (TaskPage, error){
		"plain": func(after string) (TaskPage, error) {
			return f.svc.ListTasksPage(ctx, tok, cid, "", after, 1000)
		},
		"filtered": func(after string) (TaskPage, error) {
			return f.svc.ListTasksPageFiltered(ctx, tok, cid, "", TaskFilters{Claimable: true}, after, 1000)
		},
		"legacy api": func(after string) (TaskPage, error) {
			return ListTaskPage(ctx, struct{ TaskAPI }{f.svc}, tok, cid, "", after, 1000)
		},
	}
	for name, page := range read {
		t.Run(name, func(t *testing.T) {
			var sizes []int
			after, seen := "", 0
			for {
				p, err := page(after)
				if err != nil {
					t.Fatal(err)
				}
				if len(p.Tasks) == 0 || len(p.Tasks) > 1 && encodedLen(t, p.Tasks) > ListPageBytes {
					t.Fatalf("page of %d tasks, %d bytes", len(p.Tasks), encodedLen(t, p.Tasks))
				}
				sizes = append(sizes, len(p.Tasks))
				seen += len(p.Tasks)
				if !p.HasMore {
					break
				}
				after = p.NextAfter
			}
			// The oversized legacy task is returned alone; then two per page.
			if fmt.Sprint(sizes) != "[1 2 2 1]" || seen != 6 {
				t.Fatalf("page sizes %v", sizes)
			}
		})
	}
}

func TestTaskArraysOverByteCapFail(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	for i := 0; i < 9; i++ {
		if err := f.createTask(TaskInput{Title: fmt.Sprint(i), Body: strings.Repeat("b", MaxTaskBodyBytes)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.svc.ListTasks(ctx, f.admin.Token, f.context.ID, "", 100); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "lower limit") {
		t.Fatalf("oversized array: %v", err)
	}
	if _, err := f.svc.ListTasksFiltered(ctx, f.admin.Token, f.context.ID, "", TaskFilters{Claimable: true}, 100); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversized filtered array: %v", err)
	}
	if tasks, err := f.svc.ListTasks(ctx, f.admin.Token, f.context.ID, "", 7); err != nil || len(tasks) != 7 {
		t.Fatalf("array within cap: n=%d err=%v", len(tasks), err)
	}
	if tasks, err := f.svc.ListTasksFiltered(ctx, f.admin.Token, f.context.ID, "", TaskFilters{Claimable: true}, 7); err != nil || len(tasks) != 7 {
		t.Fatalf("filtered array within cap: n=%d err=%v", len(tasks), err)
	}
}

func TestParticipantPagesStopAtByteBudget(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	p, err := f.svc.CreatePrincipal(ctx, f.admin.Token, strings.Repeat("n", 600<<10))
	if err != nil {
		t.Fatal(err)
	}
	err = f.store.Update(ctx, func(w WriteStore) error {
		for i := 0; i < 3; i++ {
			sid := fmt.Sprintf("big-%d", i)
			if err := w.PutSession(Session{ID: sid, PrincipalID: p.ID, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}, tokenHash(sid)); err != nil {
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
	after, seen := "", map[string]bool{}
	for {
		page, err := f.svc.ListParticipantsPage(ctx, f.admin.Token, f.context.ID, after, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Participants) == 0 || len(page.Participants) > 1 && encodedLen(t, page.Participants) > ListPageBytes {
			t.Fatalf("page of %d participants", len(page.Participants))
		}
		for _, participant := range page.Participants {
			if seen[participant.SessionID] {
				t.Fatalf("duplicate %s", participant.SessionID)
			}
			seen[participant.SessionID] = true
		}
		if !page.HasMore {
			break
		}
		after = page.NextAfter
	}
	if len(seen) != 4 {
		t.Fatalf("participants %v", seen)
	}
}
