package comitia

import (
	"context"
	"time"
)

// TaskFilters narrow task reads without changing stored status or ownership.
type TaskFilters struct {
	Claimable     bool   `json:"claimable,omitempty"`
	OwnerSession  string `json:"owner_session,omitempty"`
	OwnerInstance string `json:"owner_instance,omitempty"`
	Mine          bool   `json:"mine,omitempty"`
}

// TaskFilterAPI is optional; existing task interfaces retain their method sets.
type TaskFilterAPI interface {
	ListTasksFiltered(context.Context, string, string, TaskStatus, TaskFilters, int) ([]Task, error)
	ListTasksPageFiltered(context.Context, string, string, TaskStatus, TaskFilters, string, int) (TaskPage, error)
}

// TaskFilterStore optionally narrows candidates before service lease checks.
// It applies status, claimable statuses and owner IDs, with an exclusive cursor;
// the service decides whether each lease is still live.
type TaskFilterStore interface {
	TasksFilteredAfter(string, TaskStatus, TaskFilters, string, string, int) ([]Task, error)
}

func (f TaskFilters) empty() bool { return f == (TaskFilters{}) }

// WithInstance scopes mine to a configured process when no instance was supplied.
func (f TaskFilters) WithInstance(d InstanceDefaults) TaskFilters {
	if f.Mine && f.OwnerInstance == "" {
		if owner := d.Message(nil); owner != nil {
			f.OwnerInstance = owner.ID
		}
	}
	return f
}

func ListTasksFiltered(ctx context.Context, api TaskAPI, raw, cid string, status TaskStatus, f TaskFilters, limit int) ([]Task, error) {
	if f.empty() {
		return api.ListTasks(ctx, raw, cid, status, limit)
	}
	filtered, ok := api.(TaskFilterAPI)
	if !ok {
		return nil, Invalidf("task filters unsupported by API")
	}
	return filtered.ListTasksFiltered(ctx, raw, cid, status, f, limit)
}
func ListTaskPageFiltered(ctx context.Context, api TaskAPI, raw, cid string, status TaskStatus, f TaskFilters, after string, limit int) (TaskPage, error) {
	if f.empty() {
		return ListTaskPage(ctx, api, raw, cid, status, after, limit)
	}
	filtered, ok := api.(TaskFilterAPI)
	if !ok {
		return TaskPage{}, Invalidf("task filters unsupported by API")
	}
	return filtered.ListTasksPageFiltered(ctx, raw, cid, status, f, after, limit)
}

func (s *Service) taskLeaseView(r ReadStore, t Task, now time.Time) (Task, error) {
	t.LeaseTokenHash = ""
	t.LeaseExpired = false
	if t.Status != TaskLeased {
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

func (s *Service) ListTasksFiltered(ctx context.Context, raw, cid string, status TaskStatus, f TaskFilters, limit int) ([]Task, error) {
	if f.empty() {
		return s.ListTasks(ctx, raw, cid, status, limit)
	}
	page, err := s.ListTasksPageFiltered(ctx, raw, cid, status, f, "", limit)
	return page.Tasks, err
}
func (s *Service) ListTasksPageFiltered(ctx context.Context, raw, cid string, status TaskStatus, f TaskFilters, after string, limit int) (TaskPage, error) {
	if f.empty() {
		return s.ListTasksPage(ctx, raw, cid, status, after, limit)
	}
	cursor, err := taskPageCursor(status, after, limit)
	if err != nil {
		return TaskPage{}, err
	}
	if f.Mine && f.OwnerSession != "" {
		return TaskPage{}, Invalidf("mine excludes owner_session")
	}
	if len(f.OwnerSession) > 128 {
		return TaskPage{}, Invalidf("owner_session must be at most 128 bytes")
	}
	if f.OwnerInstance != "" {
		if _, err := normalizeAgentInstance(&AgentInstance{ID: f.OwnerInstance}); err != nil {
			return TaskPage{}, err
		}
	}
	page := TaskPage{Tasks: []Task{}, NextAfter: after}
	err = s.store.View(ctx, func(r ReadStore) error {
		a, _, err := s.require(r, raw, cid, PermissionRead)
		if err != nil {
			return err
		}
		if f.Mine {
			f.OwnerSession = a.session.ID
		}
		now := s.now().UTC()
		pages, paged := r.(TaskPageStore)
		filtered, narrowed := r.(TaskFilterStore)
		for {
			var tasks []Task
			if narrowed {
				tasks, err = filtered.TasksFilteredAfter(cid, status, f, cursor.CreatedAt, cursor.ID, 1000)
			} else if paged {
				tasks, err = pages.TasksAfter(cid, status, cursor.CreatedAt, cursor.ID, 1000)
			} else {
				tasks, err = r.Tasks(cid, status, 1000)
				if err == nil {
					var legacy TaskPage
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
				if f.Claimable && task.Status != TaskPending && !(task.Status == TaskLeased && task.LeaseExpired) {
					continue
				}
				if f.OwnerSession != "" || f.OwnerInstance != "" {
					if task.Status != TaskLeased || task.LeaseExpired {
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
			cursor = taskCursor{ts(last.CreatedAt), last.ID}
		}
	})
	if err != nil {
		return TaskPage{}, err
	}
	return page, nil
}

var _ TaskFilterAPI = (*Service)(nil)

func (s *Service) taskPageLeaseView(r ReadStore, page *TaskPage) error {
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
