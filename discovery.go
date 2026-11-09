package comitia

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sort"
	"time"
)

// Discovery cursors are exclusive and never acknowledge messages. Task cursors
// encode the immutable creation time and ID, so deleting the task is safe.
type TaskPage struct {
	Tasks     []Task `json:"tasks"`
	NextAfter string `json:"next_after"`
	HasMore   bool   `json:"has_more"`
}
type ParticipantPage struct {
	Participants []Participant `json:"participants"`
	NextAfter    string        `json:"next_after"`
	HasMore      bool          `json:"has_more"`
}

// Paging is optional: implementations of the original API/store contracts
// remain valid. The built-in Service, HTTP client and SQLite reads support it.
type TaskPageAPI interface {
	ListTasksPage(context.Context, string, string, TaskStatus, string, int) (TaskPage, error)
}
type ParticipantPageAPI interface {
	ListParticipantsPage(context.Context, string, string, string, int) (ParticipantPage, error)
}
type TaskPageStore interface {
	TasksAfter(string, TaskStatus, string, string, int) ([]Task, error)
}
type ParticipantPageStore interface {
	ParticipantsAfter(string, time.Time, string, int) ([]Participant, error)
}

// ListPageBytes softly bounds the encoded items array of task and participant
// pages: a page stops early with has_more, but always returns its first item
// so readers can advance. MaxListBytes bounds legacy task arrays, which have
// no continuation; a larger array of several tasks fails as invalid.
const (
	ListPageBytes = 1 << 20
	MaxListBytes  = 8 << 20
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

func boundTaskPage(page TaskPage, err error) (TaskPage, error) {
	if err != nil {
		return TaskPage{}, err
	}
	n, err := boundedCount(page.Tasks, ListPageBytes)
	if err != nil || n == len(page.Tasks) {
		return page, err
	}
	page.Tasks, page.HasMore = page.Tasks[:n], true
	page.NextAfter = encodeTaskCursor(page.Tasks[n-1])
	return page, nil
}

func boundParticipantPage(page ParticipantPage, err error) (ParticipantPage, error) {
	if err != nil {
		return ParticipantPage{}, err
	}
	n, err := boundedCount(page.Participants, ListPageBytes)
	if err != nil || n == len(page.Participants) {
		return page, err
	}
	page.Participants, page.HasMore = page.Participants[:n], true
	page.NextAfter = page.Participants[n-1].SessionID
	return page, nil
}

func checkTaskArray(tasks []Task, err error) ([]Task, error) {
	if err != nil || len(tasks) < 2 {
		return tasks, err
	}
	if n, err := boundedCount(tasks, MaxListBytes); err != nil || n < len(tasks) {
		if err == nil {
			err = Invalidf("task list exceeds %d bytes; lower limit or request a page (page/--page)", MaxListBytes)
		}
		return nil, err
	}
	return tasks, nil
}

type taskCursor struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

func encodeTaskCursor(t Task) string {
	raw, _ := json.Marshal(taskCursor{ts(t.CreatedAt), t.ID})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeTaskCursor(after string) (taskCursor, error) {
	var c taskCursor
	if after == "" {
		return c, nil
	}
	if len(after) > 684 {
		return c, Invalidf("invalid task cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(after)
	if err != nil || len(raw) > 512 {
		return c, Invalidf("invalid task cursor")
	}
	if err = json.Unmarshal(raw, &c); err != nil || c.ID == "" || len(c.ID) > 128 {
		return c, Invalidf("invalid task cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, c.CreatedAt)
	if err != nil || ts(at) != c.CreatedAt {
		return c, Invalidf("invalid task cursor creation time")
	}
	return c, nil
}
func (s *Service) ListTasksPage(ctx context.Context, token, cid string, status TaskStatus, after string, limit int) (TaskPage, error) {
	page := TaskPage{Tasks: []Task{}, NextAfter: after}
	cursor, err := taskPageCursor(status, after, limit)
	if err != nil {
		return TaskPage{}, err
	}
	err = s.store.View(ctx, func(r ReadStore) error {
		if _, _, err := s.require(r, token, cid, PermissionRead); err != nil {
			return err
		}
		pages, ok := r.(TaskPageStore)
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
func (s *Service) ListParticipantsPage(ctx context.Context, token, cid, after string, limit int) (ParticipantPage, error) {
	page := ParticipantPage{Participants: []Participant{}, NextAfter: after}
	if err := validateParticipantPage(after, limit); err != nil {
		return ParticipantPage{}, err
	}
	err := s.store.View(ctx, func(r ReadStore) error {
		if _, _, err := s.require(r, token, cid, PermissionRead); err != nil {
			return err
		}
		pages, ok := r.(ParticipantPageStore)
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

func taskPageCursor(status TaskStatus, after string, limit int) (taskCursor, error) {
	if limit < 1 || limit > 1000 {
		return taskCursor{}, Invalidf("limit must be between 1 and 1000")
	}
	if !validTaskStatusFilter(status) {
		return taskCursor{}, Invalidf("status must be pending, leased, completed, failed or cancelled")
	}
	return decodeTaskCursor(after)
}
func validateParticipantPage(after string, limit int) error {
	if limit < 1 || limit > 1000 {
		return Invalidf("limit must be between 1 and 1000")
	}
	if len(after) > 128 {
		return Invalidf("invalid participant cursor")
	}
	return nil
}

// Legacy lists have no continuation. A short maximum-sized list proves the
// directory is complete; at the ceiling, reject paging rather than hide a tail.
func legacyTaskPage(tasks []Task, after string, cursor taskCursor, limit int, source string) (TaskPage, error) {
	if len(tasks) >= 1000 {
		return TaskPage{}, Invalidf("task pagination beyond the legacy limit is unsupported by this %s", source)
	}
	tasks = append([]Task{}, tasks...)
	sort.Slice(tasks, func(i, j int) bool {
		a, b := ts(tasks[i].CreatedAt), ts(tasks[j].CreatedAt)
		return a < b || a == b && tasks[i].ID < tasks[j].ID
	})
	page := TaskPage{Tasks: []Task{}, NextAfter: after}
	for _, task := range tasks {
		created := ts(task.CreatedAt)
		if after != "" && (created < cursor.CreatedAt || created == cursor.CreatedAt && task.ID <= cursor.ID) {
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
func legacyParticipantPage(participants []Participant, after string, limit int, source string) (ParticipantPage, error) {
	if len(participants) >= 1000 {
		return ParticipantPage{}, Invalidf("participant pagination beyond the legacy limit is unsupported by this %s", source)
	}
	participants = append([]Participant{}, participants...)
	sort.Slice(participants, func(i, j int) bool { return participants[i].SessionID < participants[j].SessionID })
	page := ParticipantPage{Participants: []Participant{}, NextAfter: after}
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

func ListTaskPage(ctx context.Context, api TaskAPI, token, cid string, status TaskStatus, after string, limit int) (TaskPage, error) {
	if pages, ok := api.(TaskPageAPI); ok {
		return pages.ListTasksPage(ctx, token, cid, status, after, limit)
	}
	cursor, err := taskPageCursor(status, after, limit)
	if err != nil {
		return TaskPage{}, err
	}
	tasks, err := api.ListTasks(ctx, token, cid, status, 1000)
	if err != nil {
		return TaskPage{}, err
	}
	return boundTaskPage(legacyTaskPage(tasks, after, cursor, limit, "API"))
}
func ListParticipantPage(ctx context.Context, api MessageAPI, token, cid, after string, limit int) (ParticipantPage, error) {
	if pages, ok := api.(ParticipantPageAPI); ok {
		return pages.ListParticipantsPage(ctx, token, cid, after, limit)
	}
	if err := validateParticipantPage(after, limit); err != nil {
		return ParticipantPage{}, err
	}
	participants, err := api.ListParticipants(ctx, token, cid, 1000)
	if err != nil {
		return ParticipantPage{}, err
	}
	return boundParticipantPage(legacyParticipantPage(participants, after, limit, "API"))
}
