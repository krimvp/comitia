package comitia

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
	if limit < 1 || limit > 1000 {
		return TaskPage{}, Invalidf("limit must be between 1 and 1000")
	}
	if status != "" && status != TaskPending && status != TaskLeased && status != TaskCompleted {
		return TaskPage{}, Invalidf("status must be pending, leased or completed")
	}
	cursor, err := decodeTaskCursor(after)
	if err != nil {
		return TaskPage{}, err
	}
	err = s.store.View(ctx, func(r ReadStore) error {
		if _, _, err := s.require(r, token, cid, PermissionRead); err != nil {
			return err
		}
		tasks, err := r.TasksAfter(cid, status, cursor.CreatedAt, cursor.ID, limit+1)
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
		return nil
	})
	if err != nil {
		return TaskPage{}, err
	}
	return page, nil
}
func (s *Service) ListParticipantsPage(ctx context.Context, token, cid, after string, limit int) (ParticipantPage, error) {
	page := ParticipantPage{Participants: []Participant{}, NextAfter: after}
	if limit < 1 || limit > 1000 {
		return ParticipantPage{}, Invalidf("limit must be between 1 and 1000")
	}
	if len(after) > 128 {
		return ParticipantPage{}, Invalidf("invalid participant cursor")
	}
	err := s.store.View(ctx, func(r ReadStore) error {
		if _, _, err := s.require(r, token, cid, PermissionRead); err != nil {
			return err
		}
		participants, err := r.ParticipantsAfter(cid, s.now().UTC(), after, limit+1)
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
	if err != nil {
		return ParticipantPage{}, err
	}
	return page, nil
}
