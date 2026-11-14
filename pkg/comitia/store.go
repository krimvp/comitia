package comitia

import (
	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/sqlstore"
)

// Store interfaces, the shared grant policy for external stores, and the SQLite
// store.

// Store supplies atomic persistence.
type Store = model.Store

type ReadStore = model.ReadStore

type WriteStore = model.WriteStore

type GrantReadStore = model.GrantReadStore

type GrantWriteStore = model.GrantWriteStore

type SessionReadStore = model.SessionReadStore

type SessionWriteStore = model.SessionWriteStore

// BatchContextReadStore is optional.
type BatchContextReadStore = model.BatchContextReadStore

type ContextReadStore = model.ContextReadStore

type ContextWriteStore = model.ContextWriteStore

type ReferenceListStore = model.ReferenceListStore

type MessageReadStore = model.MessageReadStore

type MessageWriteStore = model.MessageWriteStore

// MessageQueryStore opts into the ReplyTo, ThreadID and FromSession filters.
type MessageQueryStore = model.MessageQueryStore

// MessageReadOptionsStore opts into the ExcludeSelf and Schemas predicates.
type MessageReadOptionsStore = model.MessageReadOptionsStore

// MessageScanStore is an optional ReadStore capability for filtered reads.
type MessageScanStore = model.MessageScanStore

// TaskFilterStore optionally narrows candidates before service lease checks.
type TaskFilterStore = model.TaskFilterStore

type TaskReceiptReadStore = model.TaskReceiptReadStore

type TaskReceiptWriteStore = model.TaskReceiptWriteStore

type TaskReadStore = model.TaskReadStore

type TaskWriteStore = model.TaskWriteStore

type InstancePageStore = model.InstancePageStore

type PresenceReadStore = model.PresenceReadStore

type PresenceWriteStore = model.PresenceWriteStore

type TaskPageStore = model.TaskPageStore

type ParticipantPageStore = model.ParticipantPageStore

// ChangeVersionStore is an optional Store capability that lets idle message
// waits skip unchanged polls.
type ChangeVersionStore = model.ChangeVersionStore

// GrantLookup supplies raw records, including expired and revoked ones.
type GrantLookup = model.GrantLookup

// GrantEvaluator shares ancestry facts within one transaction.
type GrantEvaluator = model.GrantEvaluator

func NewGrantEvaluator(lookup GrantLookup) *GrantEvaluator {
	return model.NewGrantEvaluator(lookup)
}

type SQLiteStore = sqlstore.SQLiteStore

// OpenSQLite opens a store using an explicitly registered SQLite driver.
//
// Deprecated: use github.com/krimvp/comitia/pkg/sqlite.OpenSQLite, which supplies
// the driver. Remote clients importing comitia do not register a driver.
func OpenSQLite(path string) (*SQLiteStore, error) {
	return sqlstore.OpenSQLite(path)
}
