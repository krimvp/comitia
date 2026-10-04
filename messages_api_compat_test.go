package comitia

import (
	"context"
	"time"
)

// Freeze the pre-discovery method sets so new capabilities stay optional.
type legacyMessageAPI interface {
	ListParticipants(context.Context, string, string, int) ([]Participant, error)
	AppendMessage(context.Context, string, string, MessageInput) (Message, error)
	ReplayMessages(context.Context, string, string, int64, int, ...MessageFilters) ([]Message, error)
	AckMessages(context.Context, string, string, int64, ...string) error
	MessageCursor(context.Context, string, string, ...string) (int64, error)
	WaitMessages(context.Context, string, string, int64, time.Duration, ...MessageFilters) ([]Message, error)
}
type legacyMessageReadStore interface {
	Message(string) (Message, error)
	Participants(string, time.Time, int) ([]Participant, error)
	MessageByDedupe(string, string, string, ...string) (Message, error)
	AckCursor(string, string, string) (int64, error)
	Messages(string, string, int64, int, ...MessageFilters) ([]Message, error)
}

var _ MessageAPI = legacyMessageAPI(nil)
var _ MessageReadStore = legacyMessageReadStore(nil)
var _ MessageSchemaAPI = (*Service)(nil)
