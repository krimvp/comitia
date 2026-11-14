package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
	"time"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/schema"
)

// boundedCount returns how many leading items fit budget encoded bytes.
func boundedCount[T any](items []T, budget int) (int, error) {
	used := 2
	for i, item := range items {
		raw, err := json.Marshal(item)
		if err != nil {
			return 0, err
		}
		n := len(raw)
		if i > 0 {
			n++
			if used+n > budget {
				return i, nil
			}
		}
		used += n
	}
	return len(items), nil
}

func boundTaskPage(page model.TaskPage, err error) (model.TaskPage, error) {
	if err != nil {
		return model.TaskPage{}, err
	}
	n, err := boundedCount(page.Tasks, model.ListPageBytes)
	if err != nil || n == len(page.Tasks) {
		return page, err
	}
	page.Tasks, page.HasMore = page.Tasks[:n], true
	page.NextAfter = encodeTaskCursor(page.Tasks[n-1])
	return page, nil
}

func boundParticipantPage(page model.ParticipantPage, err error) (model.ParticipantPage, error) {
	if err != nil {
		return model.ParticipantPage{}, err
	}
	n, err := boundedCount(page.Participants, model.ListPageBytes)
	if err != nil || n == len(page.Participants) {
		return page, err
	}
	page.Participants, page.HasMore = page.Participants[:n], true
	page.NextAfter = page.Participants[n-1].SessionID
	return page, nil
}

func checkTaskArray(tasks []model.Task, err error) ([]model.Task, error) {
	if err != nil || len(tasks) < 2 {
		return tasks, err
	}
	if n, err := boundedCount(tasks, model.MaxListBytes); err != nil || n < len(tasks) {
		if err == nil {
			err = model.Invalidf("task list exceeds %d bytes; lower limit or request a page (page/--page)", model.MaxListBytes)
		}
		return nil, err
	}
	return tasks, nil
}

type taskCursor struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func encodeTaskCursor(t model.Task) string { return encodeCreatedCursor(t.CreatedAt, t.ID) }

func encodeCreatedCursor(at time.Time, id string) string {
	raw, _ := json.Marshal(taskCursor{model.TS(at), id})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeTaskCursor(after string) (taskCursor, error) { return decodeCreatedCursor(after, "task") }

// decodeCreatedCursor reads an exclusive (created_at, id) cursor; name labels errors.
func decodeCreatedCursor(after, name string) (taskCursor, error) {
	var c taskCursor
	if after == "" {
		return c, nil
	}
	if len(after) > 684 {
		return c, model.Invalidf("invalid %s cursor", name)
	}
	raw, err := base64.RawURLEncoding.DecodeString(after)
	if err != nil || len(raw) > 512 {
		return c, model.Invalidf("invalid %s cursor", name)
	}
	if err = json.Unmarshal(raw, &c); err != nil || c.ID == "" || len(c.ID) > 128 {
		return c, model.Invalidf("invalid %s cursor", name)
	}
	at, err := time.Parse(time.RFC3339Nano, c.CreatedAt)
	if err != nil || model.TS(at) != c.CreatedAt {
		return c, model.Invalidf("invalid %s cursor creation time", name)
	}
	return c, nil
}

func (s *Service) ListTasksPage(ctx context.Context, token, cid string, status model.TaskStatus, after string, limit int) (model.TaskPage, error) {
	page := model.TaskPage{Tasks: []model.Task{}, NextAfter: after}
	cursor, err := taskPageCursor(status, after, limit)
	if err != nil {
		return model.TaskPage{}, err
	}
	err = s.store.View(ctx, func(r model.ReadStore) error {
		if _, _, err := s.require(r, token, cid, model.PermissionRead); err != nil {
			return err
		}
		pages, ok := r.(model.TaskPageStore)
		if !ok {
			tasks, err := r.Tasks(cid, status, 1000)
			if err != nil {
				return err
			}
			page, err = legacyTaskPage(tasks, after, cursor, limit, "store")
			if err != nil {
				return err
			}
			return s.taskPageLeaseView(r, &page)
		}
		tasks, err := pages.TasksAfter(cid, status, cursor.CreatedAt, cursor.ID, limit+1)
		if err != nil {
			return err
		}
		page.HasMore = len(tasks) > limit
		if page.HasMore {
			tasks = tasks[:limit]
		}
		if len(tasks) > 0 {
			page.Tasks = tasks
			page.NextAfter = encodeTaskCursor(tasks[len(tasks)-1])
		}
		return s.taskPageLeaseView(r, &page)
	})
	return boundTaskPage(page, err)
}

func (s *Service) ListParticipantsPage(ctx context.Context, token, cid, after string, limit int) (model.ParticipantPage, error) {
	page := model.ParticipantPage{Participants: []model.Participant{}, NextAfter: after}
	if err := validateParticipantPage(after, limit); err != nil {
		return model.ParticipantPage{}, err
	}
	err := s.store.View(ctx, func(r model.ReadStore) error {
		if _, _, err := s.require(r, token, cid, model.PermissionRead); err != nil {
			return err
		}
		pages, ok := r.(model.ParticipantPageStore)
		if !ok {
			participants, err := r.Participants(cid, s.now().UTC(), 1000)
			if err != nil {
				return err
			}
			page, err = legacyParticipantPage(participants, after, limit, "store")
			return err
		}
		participants, err := pages.ParticipantsAfter(cid, s.now().UTC(), after, limit+1)
		if err != nil {
			return err
		}
		page.HasMore = len(participants) > limit
		if page.HasMore {
			participants = participants[:limit]
		}
		if len(participants) > 0 {
			page.Participants = participants
			page.NextAfter = participants[len(participants)-1].SessionID
		}
		return nil
	})
	return boundParticipantPage(page, err)
}

func taskPageCursor(status model.TaskStatus, after string, limit int) (taskCursor, error) {
	if limit < 1 || limit > 1000 {
		return taskCursor{}, model.Invalidf("limit must be between 1 and 1000")
	}
	if !schema.ValidTaskStatusFilter(status) {
		return taskCursor{}, model.Invalidf("status must be pending, leased, completed, failed or cancelled")
	}
	return decodeTaskCursor(after)
}

func validateParticipantPage(after string, limit int) error {
	if limit < 1 || limit > 1000 {
		return model.Invalidf("limit must be between 1 and 1000")
	}
	if len(after) > 128 {
		return model.Invalidf("invalid participant cursor")
	}
	return nil
}

// Legacy lists have no continuation. A short maximum-sized list proves the
// directory is complete; at the ceiling, reject paging rather than hide a tail.
func legacyTaskPage(tasks []model.Task, after string, cursor taskCursor, limit int, source string) (model.TaskPage, error) {
	if len(tasks) >= 1000 {
		return model.TaskPage{}, model.Invalidf("task pagination beyond the legacy limit is unsupported by this %s", source)
	}
	tasks = append([]model.Task{}, tasks...)
	sort.Slice(tasks, func(i, j int) bool {
		a, b := model.SortableTS(tasks[i].CreatedAt), model.SortableTS(tasks[j].CreatedAt)
		return a < b || a == b && tasks[i].ID < tasks[j].ID
	})
	page := model.TaskPage{Tasks: []model.Task{}, NextAfter: after}
	at := model.SortableTS(model.ParseTime(cursor.CreatedAt))
	for _, task := range tasks {
		created := model.SortableTS(task.CreatedAt)
		if after != "" && (created < at || created == at && task.ID <= cursor.ID) {
			continue
		}
		if len(page.Tasks) == limit {
			page.HasMore = true
			break
		}
		task.LeaseTokenHash = ""
		page.Tasks = append(page.Tasks, task)
		page.NextAfter = encodeTaskCursor(task)
	}
	return page, nil
}

func legacyParticipantPage(participants []model.Participant, after string, limit int, source string) (model.ParticipantPage, error) {
	if len(participants) >= 1000 {
		return model.ParticipantPage{}, model.Invalidf("participant pagination beyond the legacy limit is unsupported by this %s", source)
	}
	participants = append([]model.Participant{}, participants...)
	sort.Slice(participants, func(i, j int) bool { return participants[i].SessionID < participants[j].SessionID })
	page := model.ParticipantPage{Participants: []model.Participant{}, NextAfter: after}
	for _, participant := range participants {
		if participant.SessionID <= after {
			continue
		}
		if len(page.Participants) == limit {
			page.HasMore = true
			break
		}
		page.Participants = append(page.Participants, participant)
		page.NextAfter = participant.SessionID
	}
	return page, nil
}

func ListTaskPage(ctx context.Context, api model.TaskAPI, token, cid string, status model.TaskStatus, after string, limit int) (model.TaskPage, error) {
	if pages, ok := api.(model.TaskPageAPI); ok {
		return pages.ListTasksPage(ctx, token, cid, status, after, limit)
	}
	cursor, err := taskPageCursor(status, after, limit)
	if err != nil {
		return model.TaskPage{}, err
	}
	tasks, err := api.ListTasks(ctx, token, cid, status, 1000)
	if err != nil {
		return model.TaskPage{}, err
	}
	return boundTaskPage(legacyTaskPage(tasks, after, cursor, limit, "API"))
}

func ListParticipantPage(ctx context.Context, api model.MessageAPI, token, cid, after string, limit int) (model.ParticipantPage, error) {
	if pages, ok := api.(model.ParticipantPageAPI); ok {
		return pages.ListParticipantsPage(ctx, token, cid, after, limit)
	}
	if err := validateParticipantPage(after, limit); err != nil {
		return model.ParticipantPage{}, err
	}
	participants, err := api.ListParticipants(ctx, token, cid, 1000)
	if err != nil {
		return model.ParticipantPage{}, err
	}
	return boundParticipantPage(legacyParticipantPage(participants, after, limit, "API"))
}
