package comitia

import (
	"context"
	"time"
)

// BatchContextReadStore is optional. Results must match individual Grants and
// ContextArchivedAt calls in the same transaction at now, including empty grant
// slices and nil archive times. Batches contain at most 1000 context IDs.
type BatchContextReadStore interface {
	GrantsForContexts(string, []string, time.Time) (map[string][]Grant, error)
	ContextArchiveTimes([]string, time.Time) (map[string]*time.Time, error)
}

type Workspace struct {
	ID, Name  string
	CreatedAt time.Time
}

type WorkContext struct {
	ID, WorkspaceID, Name string
	// Child contexts record their parent, the parent task they serve and their creator; set once at creation.
	ParentContextID, TaskID, CreatorSessionID string
	// ParentReadable lets sessions with read on the parent read this child too. Children are private otherwise.
	ParentReadable bool
	CreatedAt      time.Time
	Archived       bool
	ArchivedAt     *time.Time
}

type Reference struct {
	ID, ContextID, Kind, URI string
	CreatedAt                time.Time
}

type CreateContextInput struct{ WorkspaceID, Name string }

type CreateChildContextInput struct {
	ParentContextID, Name, TaskID string
	ParentReadable                bool
}

type AddReferenceInput struct{ ContextID, Kind, URI string }

type ContextAPI interface {
	ListContexts(context.Context, string, string, int) ([]WorkContext, error)
	CreateWorkspace(context.Context, string, string) (Workspace, error)
	CreateContext(context.Context, string, CreateContextInput) (WorkContext, error)
	CreateChildContext(context.Context, string, CreateChildContextInput) (WorkContext, error)
	ListChildContexts(context.Context, string, string, string, int) ([]WorkContext, error)
	GetContext(context.Context, string, string) (WorkContext, error)
	AddReference(context.Context, string, AddReferenceInput) (Reference, error)
	ArchiveContext(context.Context, string, string) error
}

type ContextReadStore interface {
	ContextCandidates(string, string, int) ([]WorkContext, error)
	Workspace(string) (Workspace, error)
	Context(string) (WorkContext, error)
	ChildContexts(string, string, int) ([]WorkContext, error)
	// ContextArchivedAt returns the earliest explicit archive or creator-grant
	// ancestry expiry/revocation at/before now, or nil while active. Explicit
	// archives apply immediately. Cyclic/missing creator ancestry returns
	// ErrInvalid/ErrNotFound. Archival is
	// independent of other grants and does not remove read access.
	ContextArchivedAt(string, time.Time) (*time.Time, error)
	ChildContextCount(string) (int, error)
}

type ContextWriteStore interface {
	PutWorkspace(Workspace) error
	PutContext(WorkContext) error
	PutReference(Reference) error
	PutContextCreator(string, string, string) error
	PutContextArchive(string, time.Time) error
}

// ReferencePage lists a context's references by creation time and ID. Cursors
// are exclusive and encode both, like task cursors.
type ReferencePage struct {
	References []Reference `json:"references"`
	NextAfter  string      `json:"next_after"`
	HasMore    bool        `json:"has_more"`
}

// Reference listing is optional: implementations of the original API/store
// contracts remain valid. The built-in Service, HTTP client and SQLite store
// support it.
type ReferenceListAPI interface {
	ListReferences(ctx context.Context, token, contextID, after string, limit int) (ReferencePage, error)
}

type ReferenceListStore interface {
	ReferencesAfter(contextID, created, afterID string, limit int) ([]Reference, error)
}
