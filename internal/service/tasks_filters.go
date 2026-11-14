package service

import (
	"context"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

// taskFiltersEmpty reports whether f selects no filter at all.
func taskFiltersEmpty(f model.TaskFilters) bool { return f == (model.TaskFilters{}) }

func ListTasksFiltered(ctx context.Context, api model.TaskAPI, raw, cid string, status model.TaskStatus, f model.TaskFilters, limit int) ([]model.Task, error) {
	if taskFiltersEmpty(f) {
		return api.ListTasks(ctx, raw, cid, status, limit)
	}
	filtered, ok := api.(model.TaskFilterAPI)
	if !ok {
		return nil, model.Invalidf("task filters unsupported by API")
	}
	return filtered.ListTasksFiltered(ctx, raw, cid, status, f, limit)
}

func ListTaskPageFiltered(ctx context.Context, api model.TaskAPI, raw, cid string, status model.TaskStatus, f model.TaskFilters, after string, limit int) (model.TaskPage, error) {
	if taskFiltersEmpty(f) {
		return ListTaskPage(ctx, api, raw, cid, status, after, limit)
	}
	filtered, ok := api.(model.TaskFilterAPI)
	if !ok {
		return model.TaskPage{}, model.Invalidf("task filters unsupported by API")
	}
	return filtered.ListTasksPageFiltered(ctx, raw, cid, status, f, after, limit)
}

func (s *Service) taskLeaseView(r model.ReadStore, t model.Task, now time.Time) (model.Task, error) {
	t.LeaseTokenHash = ""
	t.LeaseExpired = false
	if t.Status != model.TaskLeased {
		return t, nil
	}
	if t.LeaseExpiresAt == nil || !t.LeaseExpiresAt.After(now) {
		t.LeaseExpired = true
		return t, nil
	}
	live, err := s.holderCanClaim(r, t, now)
	t.LeaseExpired = !live
	return t, err
}

func (s *Service) ListTasksFiltered(ctx context.Context, raw, cid string, status model.TaskStatus, f model.TaskFilters, limit int) ([]model.Task, error) {
	if taskFiltersEmpty(f) {
		return s.ListTasks(ctx, raw, cid, status, limit)
	}
	page, err := s.listTasksPageFiltered(ctx, raw, cid, status, f, "", limit)
	return checkTaskArray(page.Tasks, err)
}

func (s *Service) ListTasksPageFiltered(ctx context.Context, raw, cid string, status model.TaskStatus, f model.TaskFilters, after string, limit int) (model.TaskPage, error) {
	if taskFiltersEmpty(f) {
		return s.ListTasksPage(ctx, raw, cid, status, after, limit)
	}
	return boundTaskPage(s.listTasksPageFiltered(ctx, raw, cid, status, f, after, limit))
}

// listTasksPageFiltered applies no byte budget; callers bound pages or arrays.
func (s *Service) listTasksPageFiltered(ctx context.Context, raw, cid string, status model.TaskStatus, f model.TaskFilters, after string, limit int) (model.TaskPage, error) {
	cursor, err := taskPageCursor(status, after, limit)
	if err != nil {
		return model.TaskPage{}, err
	}
	if f.Mine && f.OwnerSession != "" {
		return model.TaskPage{}, model.Invalidf("mine excludes owner_session")
	}
	if len(f.OwnerSession) > 128 {
		return model.TaskPage{}, model.Invalidf("owner_session must be at most 128 bytes")
	}
	if f.OwnerInstance != "" {
		if _, err := model.NormalizeAgentInstance(&model.AgentInstance{ID: f.OwnerInstance}); err != nil {
			return model.TaskPage{}, err
		}
	}
	page := model.TaskPage{Tasks: []model.Task{}, NextAfter: after}
	err = s.store.View(ctx, func(r model.ReadStore) error {
		a, _, err := s.require(r, raw, cid, model.PermissionRead)
		if err != nil {
			return err
		}
		if f.Mine {
			f.OwnerSession = a.session.ID
		}
		now := s.now().UTC()
		pages, paged := r.(model.TaskPageStore)
		filtered, narrowed := r.(model.TaskFilterStore)
		for {
			var tasks []model.Task
			if narrowed {
				tasks, err = filtered.TasksFilteredAfter(cid, status, f, cursor.CreatedAt, cursor.ID, 1000)
			} else if paged {
				tasks, err = pages.TasksAfter(cid, status, cursor.CreatedAt, cursor.ID, 1000)
			} else {
				tasks, err = r.Tasks(cid, status, 1000)
				if err == nil {
					var legacy model.TaskPage
					legacy, err = legacyTaskPage(tasks, after, cursor, 1000, "store")
					tasks = legacy.Tasks
				}
			}
			if err != nil {
				return err
			}
			for _, task := range tasks {
				task, err = s.taskLeaseView(r, task, now)
				if err != nil {
					return err
				}
				if f.Claimable && task.Status != model.TaskPending && !(task.Status == model.TaskLeased && task.LeaseExpired) {
					continue
				}
				if f.OwnerSession != "" || f.OwnerInstance != "" {
					if task.Status != model.TaskLeased || task.LeaseExpired {
						continue
					}
					if f.OwnerSession != "" && task.LeaseOwnerSessionID != f.OwnerSession {
						continue
					}
					if f.OwnerInstance != "" && (task.LeaseOwnerInstance == nil || task.LeaseOwnerInstance.ID != f.OwnerInstance) {
						continue
					}
				}
				if len(page.Tasks) == limit {
					page.HasMore = true
					return nil
				}
				page.Tasks = append(page.Tasks, task)
				page.NextAfter = encodeTaskCursor(task)
			}
			if !paged && !narrowed || len(tasks) < 1000 {
				return nil
			}
			last := tasks[len(tasks)-1]
			cursor = taskCursor{model.TS(last.CreatedAt), last.ID}
		}
	})
	if err != nil {
		return model.TaskPage{}, err
	}
	return page, nil
}

var _ model.TaskFilterAPI = (*Service)(nil)

func (s *Service) taskPageLeaseView(r model.ReadStore, page *model.TaskPage) error {
	now := s.now().UTC()
	for i := range page.Tasks {
		var err error
		page.Tasks[i], err = s.taskLeaseView(r, page.Tasks[i], now)
		if err != nil {
			return err
		}
	}
	return nil
}
