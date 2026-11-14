package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"

	"github.com/krimvp/comitia/internal/sqlstore"
)

var _ model.TaskAPI = oldTaskAPI{}
var _ model.TaskReadStore = oldTaskReader{}
var _ model.TaskPageAPI = (*Service)(nil)
var _ model.TaskFilterStore = (*sqlstore.TxStore)(nil)

type pageOnlyTaskStore struct{ model.Store }

func (s pageOnlyTaskStore) View(ctx context.Context, f func(model.ReadStore) error) error {
	return s.Store.View(ctx, func(r model.ReadStore) error {
		return f(struct {
			model.ReadStore
			model.TaskPageStore
		}{r, r.(model.TaskPageStore)})
	})
}

func TestTaskLeaseViewsAndFilters(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	create := func(title string) model.Task {
		t.Helper()
		task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: title})
		if err != nil {
			t.Fatal(err)
		}
		*f.now = f.now.Add(time.Second)
		return task
	}
	pending := create("pending")
	expired := create("expired")
	live := create("live")
	completed := create("completed")
	for _, tc := range []struct {
		task     model.Task
		lease    time.Duration
		instance string
	}{{expired, time.Second, "a"}, {live, time.Hour, "b"}, {completed, time.Hour, "b"}} {
		claim, err := f.svc.ClaimTask(ctx, f.admin.Token, tc.task.ID, tc.lease, model.AgentInstance{ID: tc.instance})
		if err != nil {
			t.Fatal(err)
		}
		if tc.task.ID == completed.ID {
			if _, err := f.svc.CompleteTask(ctx, f.admin.Token, tc.task.ID, claim.Token, claim.Task.LeaseVersion); err != nil {
				t.Fatal(err)
			}
		}
	}
	*f.now = f.now.Add(time.Second) // Equality expires a lease.
	before, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs := func(tasks []model.Task, ids ...string) {
		t.Helper()
		got := []string{}
		for _, task := range tasks {
			got = append(got, task.ID)
			if task.LeaseTokenHash != "" {
				t.Fatal("hash exposed")
			}
		}
		if !reflect.DeepEqual(got, ids) {
			t.Fatalf("IDs=%v want=%v", got, ids)
		}
	}
	for name, store := range map[string]model.Store{"sqlite": f.store, "page-only": pageOnlyTaskStore{f.store}, "legacy": legacyDiscoveryStore{f.store}} {
		t.Run(name, func(t *testing.T) {
			svc, err := New(model.Config{Store: store, Clock: f.svc.now})
			if err != nil {
				t.Fatal(err)
			}
			task, err := svc.GetTask(ctx, f.admin.Token, expired.ID)
			if err != nil || !task.LeaseExpired || task.Status != model.TaskLeased {
				t.Fatalf("get=%+v %v", task, err)
			}
			listed, err := svc.ListTasks(ctx, f.admin.Token, f.context.ID, model.TaskLeased, 10)
			if err != nil || len(listed) != 2 || !listed[0].LeaseExpired || listed[1].LeaseExpired {
				t.Fatalf("list=%+v %v", listed, err)
			}
			page, err := svc.ListTasksPage(ctx, f.admin.Token, f.context.ID, model.TaskLeased, "", 10)
			if err != nil || len(page.Tasks) != 2 || !page.Tasks[0].LeaseExpired {
				t.Fatalf("page=%+v %v", page, err)
			}
			for _, tc := range []struct {
				f   model.TaskFilters
				ids []string
			}{
				{model.TaskFilters{Claimable: true}, []string{pending.ID, expired.ID}},
				{model.TaskFilters{OwnerSession: f.admin.Session.ID}, []string{live.ID}},
				{model.TaskFilters{Mine: true}, []string{live.ID}},
				{model.TaskFilters{OwnerInstance: "b"}, []string{live.ID}},
				{model.TaskFilters{OwnerSession: f.admin.Session.ID, OwnerInstance: "a"}, []string{}},
				{model.TaskFilters{OwnerSession: "missing-session"}, []string{}},
				{model.TaskFilters{Claimable: true, Mine: true}, []string{}},
			} {
				tasks, err := svc.ListTasksFiltered(ctx, f.admin.Token, f.context.ID, "", tc.f, 10)
				if err != nil {
					t.Fatal(err)
				}
				wantIDs(tasks, tc.ids...)
				got := []model.Task{}
				after := ""
				for {
					page, err := svc.ListTasksPageFiltered(ctx, f.admin.Token, f.context.ID, "", tc.f, after, 1)
					if err != nil {
						t.Fatal(err)
					}
					got = append(got, page.Tasks...)
					if !page.HasMore {
						break
					}
					if page.NextAfter == after {
						t.Fatal("stalled cursor")
					}
					after = page.NextAfter
				}
				wantIDs(got, tc.ids...)
			}
		})
	}
	after, err := f.svc.ReplayMessages(ctx, f.admin.Token, f.context.ID, 0, 100)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("reads changed events")
	}
	if err := f.store.View(ctx, func(r model.ReadStore) error {
		task, err := r.Task(expired.ID)
		if err == nil && (task.LeaseExpired || task.Status != model.TaskLeased) {
			t.Fatal("read metadata persisted")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestTaskFilterPagingPastSkippedCandidates(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	expiry := f.now.Add(time.Hour)
	err := f.store.Update(ctx, func(w model.WriteStore) error {
		for i := 0; i < 1003; i++ {
			task := model.Task{ID: fmt.Sprintf("t-%04d", i), ContextID: f.context.ID, Title: "work", Status: model.TaskLeased, LeaseOwnerSessionID: f.admin.Session.ID, LeaseExpiresAt: &expiry, CreatedAt: *f.now, UpdatedAt: *f.now, LeaseOwnerInstance: &model.AgentInstance{ID: "worker"}}
			if i >= 1001 {
				expired := *f.now
				task.LeaseExpiresAt = &expired
			}
			if err := w.PutTask(task); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, store := range []model.Store{f.store, pageOnlyTaskStore{f.store}} {
		svc, err := New(model.Config{Store: store, Clock: f.svc.now})
		if err != nil {
			t.Fatal(err)
		}
		page, err := svc.ListTasksPageFiltered(ctx, f.admin.Token, f.context.ID, "", model.TaskFilters{Claimable: true}, "", 1)
		if err != nil || len(page.Tasks) != 1 || page.Tasks[0].ID != "t-1001" || !page.HasMore {
			t.Fatalf("first=%+v %v", page, err)
		}
		page, err = svc.ListTasksPageFiltered(ctx, f.admin.Token, f.context.ID, "", model.TaskFilters{Claimable: true}, page.NextAfter, 1)
		if err != nil || len(page.Tasks) != 1 || page.Tasks[0].ID != "t-1002" || page.HasMore {
			t.Fatalf("last=%+v %v", page, err)
		}
	}
	legacy, err := New(model.Config{Store: legacyDiscoveryStore{f.store}, Clock: f.svc.now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.ListTasksFiltered(ctx, f.admin.Token, f.context.ID, "", model.TaskFilters{Mine: true}, 1); !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal(err)
	}
}

func TestTaskFiltersValidationAndPrivacy(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	for _, tc := range []struct {
		f      model.TaskFilters
		status model.TaskStatus
		after  string
		limit  int
	}{
		{model.TaskFilters{Mine: true, OwnerSession: "other"}, "", "", 1},
		{model.TaskFilters{OwnerInstance: "bad instance"}, "", "", 1},
		{model.TaskFilters{OwnerSession: strings.Repeat("x", 129)}, "", "", 1},
		{model.TaskFilters{Mine: true}, "bad", "", 1},
		{model.TaskFilters{Mine: true}, "", "bad", 1},
		{model.TaskFilters{Mine: true}, "", "", 0},
	} {
		if _, err := f.svc.ListTasksPageFiltered(ctx, f.admin.Token, f.context.ID, tc.status, tc.f, tc.after, tc.limit); !errors.Is(err, model.ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, cid := range []string{f.context.ID, "missing-context"} {
		if _, err := f.svc.ListTasksFiltered(ctx, "invalid", cid, "", model.TaskFilters{OwnerSession: "missing"}, 1); !errors.Is(err, model.ErrUnauthenticated) {
			t.Fatal(err)
		}
	}
	_, helper, _, err := f.svc.DelegateSession(ctx, f.admin.Token, model.DelegateInput{PrincipalName: "reader", TTL: time.Hour, Grants: []model.DelegatedGrant{{ContextID: f.context.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	private, err := f.svc.CreateChildContext(ctx, f.admin.Token, model.CreateChildContextInput{ParentContextID: f.context.ID, Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	hidden, err := f.svc.CreateTask(ctx, f.admin.Token, private.ID, model.TaskInput{Title: "hidden"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{hidden.ID, "missing-task"} {
		if _, err := f.svc.GetTask(ctx, helper.Token, id); !errors.Is(err, model.ErrForbidden) {
			t.Fatal(err)
		}
	}
	for _, cid := range []string{private.ID, "missing-context"} {
		if _, err := f.svc.ListTasksFiltered(ctx, helper.Token, cid, "", model.TaskFilters{OwnerSession: f.admin.Session.ID}, 1); !errors.Is(err, model.ErrForbidden) {
			t.Fatal(err)
		}
	}
	api := oldTaskAPI{f.svc}
	if _, ok := any(api).(model.TaskFilterAPI); ok {
		t.Fatal("legacy fixture exposes filters")
	}
	if _, err := ListTasksFiltered(ctx, api, f.admin.Token, f.context.ID, "", model.TaskFilters{Mine: true}, 1); !errors.Is(err, model.ErrInvalid) || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal(err)
	}
	if _, err := ListTaskPageFiltered(ctx, api, f.admin.Token, f.context.ID, "", model.TaskFilters{Mine: true}, "", 1); !errors.Is(err, model.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := ListTasksFiltered(ctx, api, f.admin.Token, f.context.ID, "", model.TaskFilters{}, 1); err != nil {
		t.Fatal(err)
	}
}

func TestTaskLeaseViewLostAuthority(t *testing.T) {
	for _, revokeSession := range []bool{false, true} {
		t.Run(fmt.Sprint(revokeSession), func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			ctx := context.Background()
			_, helper, _, err := f.svc.DelegateSession(ctx, f.admin.Token, model.DelegateInput{PrincipalName: "worker", TTL: time.Hour, Grants: []model.DelegatedGrant{{ContextID: f.context.ID, Permissions: model.PermissionRead | model.PermissionClaim, ExpiresAt: f.now.Add(time.Hour)}}})
			if err != nil {
				t.Fatal(err)
			}
			task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, model.TaskInput{Title: "work"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = f.svc.ClaimTask(ctx, helper.Token, task.ID, time.Hour); err != nil {
				t.Fatal(err)
			}
			if revokeSession {
				err = f.svc.RevokeSession(ctx, f.admin.Token, helper.Session.ID)
			} else {
				var grants []model.Grant
				err = f.store.View(ctx, func(r model.ReadStore) error {
					var e error
					grants, e = r.Grants(helper.Session.ID, f.context.ID, *f.now)
					return e
				})
				if err != nil {
					t.Fatal(err)
				}
				err = f.svc.RevokeGrant(ctx, f.admin.Token, grants[0].ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.svc.GetTask(ctx, f.admin.Token, task.ID)
			if err != nil || !got.LeaseExpired {
				t.Fatalf("got=%+v %v", got, err)
			}
			mine, err := f.svc.ListTasksFiltered(ctx, f.admin.Token, f.context.ID, "", model.TaskFilters{OwnerSession: helper.Session.ID}, 1)
			if err != nil || len(mine) != 0 {
				t.Fatalf("owner=%v %v", mine, err)
			}
			claimable, err := f.svc.ListTasksFiltered(ctx, f.admin.Token, f.context.ID, "", model.TaskFilters{Claimable: true}, 1)
			if err != nil || len(claimable) != 1 {
				t.Fatalf("claimable=%v %v", claimable, err)
			}
			if _, err = f.svc.ClaimTask(ctx, f.admin.Token, task.ID, time.Minute); err != nil {
				t.Fatal(err)
			}
		})
	}
}
