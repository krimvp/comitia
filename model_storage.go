package comitia

import (
	"context"
	"time"
)

// ChangeVersionStore is an optional Store capability that lets idle message
// waits skip unchanged polls. ChangeVersion runs outside any transaction. Its
// result must differ from every earlier result of the same store once any
// commit by this handle, another handle or another process has become visible
// to View; spurious changes are allowed. Errors make waits scan as before.
// Stores without this capability scan on every poll.
type ChangeVersionStore interface {
	ChangeVersion(context.Context) (uint64, error)
}

const StorageProbeTimeout = 250 * time.Millisecond

type StorageStatus struct {
	WriteReady                 bool   `json:"write_ready"`
	ProbeDurationMS            int64  `json:"probe_duration_ms"`
	WriteAttempts              uint64 `json:"write_attempts"`
	WriteFailures              uint64 `json:"write_failures"`
	LastWriteFailureDurationMS int64  `json:"last_write_failure_duration_ms"`
	LastWriteDurationMS        int64  `json:"last_write_duration_ms"`
	MaxWriteDurationMS         int64  `json:"max_write_duration_ms"`
	LastWriteErrorClass        string `json:"last_write_error_class,omitempty"`
}

type StorageStatusAPI interface {
	StorageStatus(context.Context, string) (StorageStatus, error)
}
