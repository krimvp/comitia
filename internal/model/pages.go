package model

import (
	"context"
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
