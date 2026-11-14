package comitia

import (
	"context"
	"encoding/json"
	"time"
)

const MaxInstanceResumeBytes = 4096

// Stores keep at most this many instances per (session, context); a new
// instance evicts that session's least recently seen row, never another's.
const MaxInstancesPerSession = 64

// Rows of revoked or expired sessions stay listed (inactive) for this long,
// then disappear from lists and are pruned by later presence writes.
const InstanceRetention = 24 * time.Hour

// Unpaged ListInstances returns at most this many rows.
const DefaultInstanceListLimit = 100

const InstanceHeartbeatInterval = 30 * time.Second

const InstanceRecentWindow = 90 * time.Second

// InstancePresence is self-reported process metadata, never proof of identity or liveness.
type InstancePresence struct {
	ID             string          `json:"id"`
	Label          string          `json:"label,omitempty"`
	SessionID      string          `json:"session_id"`
	ContextID      string          `json:"context_id"`
	LastSeen       time.Time       `json:"last_seen"`
	Resume         json.RawMessage `json:"resume,omitempty"`
	Active         bool            `json:"active"`
	SeenSecondsAgo *int64          `json:"seen_seconds_ago,omitempty"`
	Recent         *bool           `json:"recent,omitempty"`
}

// InstancePage cursors are exclusive "session_id/instance_id" pairs.
type InstancePage struct {
	Instances []InstancePresence `json:"instances"`
	NextAfter string             `json:"next_after"`
	HasMore   bool               `json:"has_more"`
}

// Paging is optional, like participant paging.
type InstancePageAPI interface {
	ListInstancesPage(context.Context, string, string, string, int) (InstancePage, error)
}

type InstancePageStore interface {
	InstancesAfter(contextID string, now time.Time, afterSession, afterInstance string, limit int) ([]InstancePresence, error)
}

type PresenceAPI interface {
	ListInstances(context.Context, string, string) ([]InstancePresence, error)
	SetInstanceResume(context.Context, string, string, *AgentInstance, json.RawMessage) error
}

type PresenceReadStore interface {
	Instances(string, time.Time) ([]InstancePresence, error)
	InstanceLastSeen(string, string, string) (time.Time, error)
}

type PresenceWriteStore interface {
	PutInstancePresence(InstancePresence) error
	// A failed heartbeat must not undo or poison the caller's transaction.
	TouchInstancePresence(InstancePresence) error
}
