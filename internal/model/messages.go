package model

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// InstanceDefaults holds validated process defaults. Its zero value preserves
// legacy session-scoped behavior. Instance metadata never grants permissions.
type InstanceDefaults struct {
	instance *AgentInstance
}

func NewInstanceDefaults(instance *AgentInstance) (InstanceDefaults, error) {
	instance, err := NormalizeAgentInstance(instance)
	return InstanceDefaults{instance: instance}, err
}

// InstanceDefaultsFromEnv captures the environment once at startup. Empty values
// disable defaults; a label without an ID and malformed values are errors.
func InstanceDefaultsFromEnv() (InstanceDefaults, error) {
	id, label := os.Getenv("COMITIA_AGENT_INSTANCE"), os.Getenv("COMITIA_AGENT_INSTANCE_LABEL")
	if id == "" && label == "" {
		return InstanceDefaults{}, nil
	}
	d, err := NewInstanceDefaults(&AgentInstance{ID: id, Label: label})
	if err != nil {
		return InstanceDefaults{}, fmt.Errorf("%w: COMITIA_AGENT_INSTANCE / COMITIA_AGENT_INSTANCE_LABEL", err)
	}
	return d, nil
}

// Message uses the complete explicit object when supplied, otherwise a copy of
// the default. An explicit ID never inherits another instance's display label.
func (d InstanceDefaults) Message(explicit *AgentInstance) *AgentInstance {
	if explicit == nil {
		explicit = d.instance
	}
	if explicit == nil {
		return nil
	}
	out := *explicit
	return &out
}

// ID resolves an optional instance argument. An explicit empty string selects
// the legacy cursor, even when a process default is configured.
func (d InstanceDefaults) ID(explicit ...string) (string, error) {
	if len(explicit) != 0 {
		return CursorInstance(explicit)
	}
	if d.instance != nil {
		return d.instance.ID, nil
	}
	return "", nil
}

type Message struct {
	TaskAttempt                               *TaskAttempt `json:"task_attempt,omitempty"`
	ID, ContextID, SessionID, DedupeKey, Body string
	Sequence                                  int64
	CreatedAt                                 time.Time
	Envelope                                  *MessageEnvelope `json:"envelope,omitempty"`
	AgentInstance                             *AgentInstance   `json:"agent_instance,omitempty"`
	AgentInstanceSelfReported                 bool             `json:"agent_instance_self_reported,omitempty"`
}

// AgentInstance is caller-claimed coordination metadata, never an authenticated identity.
type AgentInstance struct {
	ID    string `json:"id"`
	Label string `json:"label,omitempty"`
}

type MessageInput struct {
	// Top-level recipients are shorthand for envelope recipients.
	RecipientSessionIDs []string   `json:"recipient_session_ids,omitempty"`
	RecipientInstances  []string   `json:"recipient_instances,omitempty"`
	TaskFence           *TaskFence `json:"task_fence,omitempty"`
	DedupeKey, Body     string
	Envelope            *MessageEnvelope `json:"envelope,omitempty"`
	AgentInstance       *AgentInstance   `json:"agent_instance,omitempty"`
}

type MessageEnvelope struct {
	Schema              string          `json:"schema"`
	RecipientSessionIDs []string        `json:"recipient_session_ids,omitempty"`
	RecipientInstances  []string        `json:"recipient_instances,omitempty"`
	TaskID              string          `json:"task_id,omitempty"`
	ReplyTo             string          `json:"reply_to,omitempty"`
	Payload             json.RawMessage `json:"payload"`
}

// MessageFilters narrow replay within the same context read permission.
// Nonempty fields are combined with AND; routing metadata is self-reported.
type MessageFilters struct {
	// ExcludeSelf omits messages authored by this session and AgentInstance.
	ExcludeSelf bool `json:"exclude_self,omitempty"`
	// FromCursor starts at this session's acknowledgement cursor for AgentInstance.
	FromCursor bool `json:"from_cursor,omitempty"`
	// Schemas selects any of these exact schemas; combined with Schema using AND.
	Schemas []string `json:"schemas,omitempty"`
	// ReplyTo selects direct replies; ThreadID includes the root and descendants.
	ReplyTo  string `json:"reply_to,omitempty"`
	ThreadID string `json:"thread_id,omitempty"`
	// FromSession selects the authenticated author, independent of instance claims.
	FromSession string `json:"from_session,omitempty"`
	// MaxBytes bounds the encoded messages array; zero uses DefaultMessageBytes.
	MaxBytes     int    `json:"max_bytes,omitempty"`
	ForInstance  string `json:"for_instance,omitempty"`
	FromInstance string `json:"from_instance,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
	Schema       string `json:"schema,omitempty"`
	// StrictRecipients requires both nonempty recipient lists to match.
	// It is opt-in and requires ForInstance.
	StrictRecipients bool `json:"strict_recipients,omitempty"`
	// AgentInstance names the caller's own instance for presence; it never
	// identifies self and cursor scope. Adapters apply defaults when it is nil.
	AgentInstance *string `json:"agent_instance,omitempty"`
}

type PayloadValidator func(json.RawMessage) error

type Participant struct {
	SessionID, PrincipalID, Name string
	ExpiresAt                    time.Time
}

type MessageAPI interface {
	ListParticipants(context.Context, string, string, int) ([]Participant, error)
	AppendMessage(context.Context, string, string, MessageInput) (Message, error)
	ReplayMessages(context.Context, string, string, int64, int, ...MessageFilters) ([]Message, error)
	AckMessages(context.Context, string, string, int64, ...string) error
	MessageCursor(context.Context, string, string, ...string) (int64, error)
	WaitMessages(context.Context, string, string, int64, time.Duration, ...MessageFilters) ([]Message, error)
}

type MessageReadStore interface {
	Message(string) (Message, error)
	Participants(string, time.Time, int) ([]Participant, error)
	MessageByDedupe(string, string, string, ...string) (Message, error)
	AckCursor(string, string, string) (int64, error)
	Messages(string, string, int64, int, ...MessageFilters) ([]Message, error)
}

type MessageWriteStore interface {
	PutMessage(Message) (Message, error)
	Ack(string, string, int64, time.Time, string) error
}

// Instance IDs are short, printable routing keys. They confer no permissions.
func ValidInstanceID(id string) bool {
	if len(id) == 0 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func NormalizeAgentInstance(in *AgentInstance) (*AgentInstance, error) {
	if in == nil {
		return nil, nil
	}
	if !ValidInstanceID(in.ID) {
		return nil, Invalidf("agent_instance.id must be 1-64 ASCII letters, digits, dot, underscore or hyphen")
	}
	if len(in.Label) > 128 || !utf8.ValidString(in.Label) {
		return nil, Invalidf("agent_instance.label must be valid UTF-8 and at most 128 bytes")
	}
	for _, c := range in.Label {
		if unicode.IsControl(c) {
			return nil, Invalidf("agent_instance.label must not contain control characters")
		}
	}
	out := *in
	return &out, nil
}

// Omitting the instance (or supplying an empty ID) selects the legacy cursor.
func CursorInstance(instances []string) (string, error) {
	if len(instances) > 1 {
		return "", Invalidf("agent_instance accepts at most one instance")
	}
	if len(instances) == 0 || instances[0] == "" {
		return "", nil
	}
	if !ValidInstanceID(instances[0]) {
		return "", Invalidf("agent_instance must be 1-64 ASCII letters, digits, dot, underscore or hyphen")
	}
	return instances[0], nil
}

const DefaultMessageBytes = 1 << 20

const MaxMessageBytes = 4 << 20

// MessagePage preserves whole messages. One message may exceed the soft byte
// budget so a reader can always advance. NextAfter is the last returned
// sequence. ScannedThrough also covers skipped nonmatches in the same snapshot;
// resume with it only for the same filters. Neither cursor acknowledges rows.
type MessagePage struct {
	Messages       []Message `json:"messages"`
	NextAfter      int64     `json:"next_after"`
	ScannedThrough int64     `json:"scanned_through"`
	HasMore        bool      `json:"has_more"`
}

// MessagePageAPI is optional so existing embedded implementations remain valid.
type MessagePageAPI interface {
	ReplayMessagesPage(context.Context, string, string, int64, int, ...MessageFilters) (MessagePage, error)
	WaitMessagesPage(context.Context, string, string, int64, time.Duration, ...MessageFilters) (MessagePage, error)
}

func MessageByteBudget(n int) int {
	if n == 0 {
		return DefaultMessageBytes
	}
	return n
}

// MessageQueryAPI opts into lookup and all scoped MessageFilters.
// MessageAPI's original method set is unchanged.
type MessageQueryAPI interface {
	GetMessage(context.Context, string, string, string) (Message, error)
}

// MessageQueryStore opts into the ReplyTo, ThreadID and FromSession filters.
// An older store must never silently return an unfiltered conversation.
type MessageQueryStore interface {
	SupportsMessageQueries() bool
}

func ScopedMessageFilters(f MessageFilters) bool {
	return f.ReplyTo != "" || f.ThreadID != "" || f.FromSession != ""
}

// MessageReadOptionsAPI opts into self exclusion, cursor starts and schema lists.
// Older APIs must not silently ignore these read options.
type MessageReadOptionsAPI interface {
	SupportsMessageReadOptions() bool
}

// MessageReadOptionsStore opts into the ExcludeSelf and Schemas predicates.
type MessageReadOptionsStore interface {
	SupportsMessageReadOptions() bool
}

// filterIncludesRenewals reports whether f explicitly selects task events.
func FilterIncludesRenewals(f MessageFilters) bool {
	if f.Schema == TaskEventSchema {
		return true
	}
	for _, schema := range f.Schemas {
		if schema == TaskEventSchema {
			return true
		}
	}
	return false
}

// MaxDedupeKeyBytes bounds new dedupe keys; existing longer keys stay readable.
const MaxDedupeKeyBytes = 256

// DedupeConflictError reports a send whose dedupe scope already holds a different
// message. That message belongs to the caller's own session and instance scope;
// callers that cannot read the context get a plain ErrConflict instead.
type DedupeConflictError struct {
	MessageID string
	Sequence  int64
	Differs   []string // body, envelope, agent_instance, task_attempt
}

func (e *DedupeConflictError) Error() string {
	return fmt.Sprintf("%s: dedupe_key already used by message %s (sequence %d) with a different %s", ErrConflict, e.MessageID, e.Sequence, strings.Join(e.Differs, ", "))
}

func (e *DedupeConflictError) Unwrap() error { return ErrConflict }

// TaskFence authenticates a result against the current live lease in the append transaction.
type TaskFence struct {
	TaskID       string `json:"task_id"`
	ClaimToken   string `json:"claim_token"`
	LeaseVersion int64  `json:"lease_version"`
}

// TaskAttempt is server-validated provenance. SessionID and Envelope.ReplyTo on
// the containing message identify the authenticated author and request.
type TaskAttempt struct {
	TaskID         string `json:"task_id"`
	LeaseVersion   int64  `json:"lease_version"`
	ClaimTokenHash string `json:"-"`
}

const MaxMessageWait = 60 * time.Second

// Bounds for the MessageFilters.Schemas list.
const (
	maxFilterSchemas     = 64
	maxFilterSchemaBytes = 128
)

func CombineMessageFilters(filters []MessageFilters) (MessageFilters, error) {
	if len(filters) > 1 {
		return MessageFilters{}, Invalidf("at most one filter set allowed")
	}
	var f MessageFilters
	if len(filters) == 1 {
		f = filters[0]
	}
	if f.MaxBytes < 0 || f.MaxBytes > MaxMessageBytes {
		return f, Invalidf("max_bytes must be between 0 and %d", MaxMessageBytes)
	}
	if f.StrictRecipients && f.ForInstance == "" {
		return f, Invalidf("strict_recipients requires for_instance")
	}
	if f.ForInstance != "" && !ValidInstanceID(f.ForInstance) {
		return f, Invalidf("for_instance must be 1-64 letters, digits, '.', '_' or '-'")
	}
	if f.FromInstance != "" && !ValidInstanceID(f.FromInstance) {
		return f, Invalidf("from_instance must be 1-64 letters, digits, '.', '_' or '-'")
	}
	if instance := FilterAgentInstance(f); instance != "" && !ValidInstanceID(instance) {
		return f, Invalidf("agent_instance must be 1-64 letters, digits, '.', '_' or '-'")
	}
	// Each name is a bound SQL variable; keep lists well inside driver limits.
	if len(f.Schemas) > maxFilterSchemas {
		return f, Invalidf("schemas allows at most %d entries", maxFilterSchemas)
	}
	for _, schema := range f.Schemas {
		if len(schema) > maxFilterSchemaBytes {
			return f, Invalidf("schemas entries must be at most %d bytes", maxFilterSchemaBytes)
		}
	}
	return f, nil
}

// filterAgentInstance returns the instance named by f.AgentInstance, or "".
func FilterAgentInstance(f MessageFilters) string {
	if f.AgentInstance == nil {
		return ""
	}
	return *f.AgentInstance
}

// MessageScanStore is an optional ReadStore capability for filtered reads.
// MessagesScanned applies the same filters and limits as Messages. Its watermark
// is at least after; every match in (after, watermark] must be returned.
// Matches and watermark must come from the same View snapshot. Empty reads may
// advance through nonmatches; limited reads must not skip an omitted match.
// The watermark is read progress for these filters, never an acknowledgement.
// Stores without this capability retain Messages fallback behavior.
type MessageScanStore interface {
	MessagesScanned(string, string, int64, int, MessageFilters) ([]Message, int64, error)
}
