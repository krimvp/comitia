package httpapi

import (
	"encoding/json"
	"github.com/krimvp/comitia"
	"time"
)

type principalDTO struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Admin     bool      `json:"admin"`
	CreatedAt time.Time `json:"created_at"`
}

type sessionDTO struct {
	ID          string     `json:"id"`
	PrincipalID string     `json:"principal_id"`
	ParentID    string     `json:"parent_id"`
	ExpiresAt   time.Time  `json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type workspaceDTO struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type contextDTO struct {
	ID               string     `json:"id"`
	WorkspaceID      string     `json:"workspace_id"`
	Name             string     `json:"name"`
	ParentContextID  string     `json:"parent_context_id"`
	TaskID           string     `json:"task_id"`
	CreatorSessionID string     `json:"creator_session_id"`
	ParentReadable   bool       `json:"parent_readable"`
	CreatedAt        time.Time  `json:"created_at"`
	Archived         bool       `json:"archived"`
	ArchivedAt       *time.Time `json:"archived_at"`
}

type referenceDTO struct {
	ID        string    `json:"id"`
	ContextID string    `json:"context_id"`
	Kind      string    `json:"kind"`
	URI       string    `json:"uri"`
	CreatedAt time.Time `json:"created_at"`
}

type grantDTO struct {
	ID              string             `json:"id"`
	SessionID       string             `json:"session_id"`
	ContextID       string             `json:"context_id"`
	ParentGrantID   string             `json:"parent_grant_id"`
	IssuerSessionID string             `json:"issuer_session_id"`
	Permissions     comitia.Permission `json:"permissions"`
	ExpiresAt       time.Time          `json:"expires_at"`
	RevokedAt       *time.Time         `json:"revoked_at"`
	CreatedAt       time.Time          `json:"created_at"`
}

type messageDTO struct {
	TaskAttempt               *comitia.TaskAttempt     `json:"task_attempt,omitempty"`
	ID                        string                   `json:"id"`
	ContextID                 string                   `json:"context_id"`
	SessionID                 string                   `json:"session_id"`
	DedupeKey                 string                   `json:"dedupe_key"`
	Body                      string                   `json:"body"`
	Sequence                  int64                    `json:"sequence"`
	CreatedAt                 time.Time                `json:"created_at"`
	Envelope                  *comitia.MessageEnvelope `json:"envelope,omitempty"`
	AgentInstance             *comitia.AgentInstance   `json:"agent_instance,omitempty"`
	AgentInstanceSelfReported bool                     `json:"agent_instance_self_reported,omitempty"`
}

type participantDTO struct {
	SessionID   string    `json:"session_id"`
	PrincipalID string    `json:"principal_id"`
	Name        string    `json:"name"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type taskDTO struct {
	ID                  string                 `json:"id"`
	ContextID           string                 `json:"context_id"`
	Title               string                 `json:"title"`
	Body                string                 `json:"body"`
	Status              comitia.TaskStatus     `json:"status"`
	LeaseOwnerSessionID string                 `json:"lease_owner_session_id"`
	LeaseVersion        int64                  `json:"lease_version"`
	LeaseExpiresAt      *time.Time             `json:"lease_expires_at"`
	LeaseTokenHash      string                 `json:"-"`
	CreatedAt           time.Time              `json:"created_at"`
	UpdatedAt           time.Time              `json:"updated_at"`
	LeaseOwnerInstance  *comitia.AgentInstance `json:"lease_owner_instance,omitempty"`
}

type credentialDTO struct {
	Session sessionDTO `json:"session"`
	Token   string     `json:"token"`
}

type claimDTO struct {
	Task  taskDTO `json:"task"`
	Token string  `json:"token"`
}

type pageDTO struct {
	Messages       []messageDTO `json:"messages"`
	NextAfter      int64        `json:"next_after"`
	ScannedThrough int64        `json:"scanned_through"`
	HasMore        bool         `json:"has_more"`
}

// Discovery page fields keep their snake_case names in both representations;
// only the records inside them use the negotiated spelling.
type taskPageDTO struct {
	Tasks     []taskDTO `json:"tasks"`
	NextAfter string    `json:"next_after"`
	HasMore   bool      `json:"has_more"`
}

type participantPageDTO struct {
	Participants []participantDTO `json:"participants"`
	NextAfter    string           `json:"next_after"`
	HasMore      bool             `json:"has_more"`
}

type createContextInput struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
}

type childContextInput struct {
	ParentContextID string `json:"parent_context_id"`
	Name            string `json:"name"`
	TaskID          string `json:"task_id,omitempty"`
	ParentReadable  bool   `json:"parent_readable"`
}

type referenceInput struct {
	ContextID string `json:"context_id"`
	Kind      string `json:"kind"`
	URI       string `json:"uri"`
}

type grantInput struct {
	SessionID   string             `json:"session_id"`
	ContextID   string             `json:"context_id"`
	Permissions comitia.Permission `json:"permissions"`
	ExpiresAt   time.Time          `json:"expires_at"`
}

type delegatedGrantInput struct {
	ContextID   string             `json:"context_id"`
	Permissions comitia.Permission `json:"permissions"`
	ExpiresAt   time.Time          `json:"expires_at"`
}

type delegateInput struct {
	PrincipalName string                `json:"principal_name"`
	TTLSeconds    *float64              `json:"ttl_seconds,omitempty"`
	TTL           *time.Duration        `json:"ttl,omitempty"`
	Grants        []delegatedGrantInput `json:"grants"`
}

type messageInput struct {
	TaskFence     *comitia.TaskFence       `json:"task_fence,omitempty"`
	DedupeKey     string                   `json:"dedupe_key"`
	Body          string                   `json:"body"`
	Envelope      *comitia.MessageEnvelope `json:"envelope,omitempty"`
	AgentInstance *comitia.AgentInstance   `json:"agent_instance,omitempty"`
}

type taskInput struct {
	Title       string `json:"title"`
	Body        string `json:"body"`
	OperationID string `json:"operation_id,omitempty"`
}

type emptyRequest struct {
}

type emptyResult struct {
}

type nameRequest struct {
	Name string `json:"name"`
}

type mintRequest struct {
	PrincipalID string         `json:"principal_id"`
	TTLSeconds  *float64       `json:"ttl_seconds,omitempty"`
	TTL         *time.Duration `json:"ttl,omitempty"`
}

type delegateRequest struct {
	Delegate *delegateInput `json:"delegate"`
}

type revokeSessionRequest struct {
	SessionID string `json:"session_id"`
}

type revokeGrantRequest struct {
	GrantID string `json:"grant_id"`
}

type createContextRequest struct {
	CreateContext *createContextInput `json:"create_context"`
}

type childContextRequest struct {
	ChildContext *childContextInput `json:"child_context"`
}

type contextRequest struct {
	ContextID string `json:"context_id"`
}

type contextsRequest struct {
	AfterID string `json:"after_id,omitempty"`
	Limit   *int   `json:"limit,omitempty"`
}

type childrenRequest struct {
	ContextID string `json:"context_id"`
	AfterID   string `json:"after_id,omitempty"`
	Limit     *int   `json:"limit,omitempty"`
}

type referenceRequest struct {
	Reference *referenceInput `json:"reference"`
}

type grantRequest struct {
	Grant *grantInput `json:"grant"`
}

type createTaskRequest struct {
	ContextID string     `json:"context_id"`
	Task      *taskInput `json:"task"`
}

type taskRequest struct {
	TaskID string `json:"task_id"`
}

type listTasksRequest struct {
	ContextID string             `json:"context_id"`
	Status    comitia.TaskStatus `json:"status,omitempty"`
	AfterID   string             `json:"after_id,omitempty"`
	Limit     *int               `json:"limit,omitempty"`
	Page      bool               `json:"page,omitempty"`
}

type claimRequest struct {
	TaskID        string                 `json:"task_id"`
	OperationID   string                 `json:"operation_id,omitempty"`
	LeaseSeconds  *float64               `json:"lease_seconds,omitempty"`
	TTL           *time.Duration         `json:"ttl,omitempty"`
	OpenEnded     bool                   `json:"open_ended,omitempty"`
	ClaimInstance *comitia.AgentInstance `json:"claim_instance,omitempty"`
}

type renewRequest struct {
	TaskID       string         `json:"task_id"`
	OperationID  string         `json:"operation_id,omitempty"`
	ClaimToken   string         `json:"claim_token"`
	LeaseVersion *int64         `json:"lease_version"`
	LeaseSeconds *float64       `json:"lease_seconds,omitempty"`
	TTL          *time.Duration `json:"ttl,omitempty"`
	OpenEnded    bool           `json:"open_ended,omitempty"`
}

type finishTaskRequest struct {
	TaskID       string `json:"task_id"`
	ClaimToken   string `json:"claim_token,omitempty"`
	LeaseVersion *int64 `json:"lease_version"`
}

type participantsRequest struct {
	ContextID string `json:"context_id"`
	AfterID   string `json:"after_id,omitempty"`
	Limit     *int   `json:"limit,omitempty"`
	Page      bool   `json:"page,omitempty"`
}

type appendRequest struct {
	ContextID string        `json:"context_id"`
	Message   *messageInput `json:"message"`
}

type ackRequest struct {
	ContextID     string `json:"context_id"`
	After         int64  `json:"after"`
	AgentInstance string `json:"agent_instance,omitempty"`
}

type cursorRequest struct {
	ContextID     string `json:"context_id"`
	AgentInstance string `json:"agent_instance,omitempty"`
}

type resumeRequest struct {
	ContextID string                 `json:"context_id"`
	Instance  *comitia.AgentInstance `json:"instance"`
	Resume    json.RawMessage        `json:"resume"`
}

type replayRequest struct {
	ContextID     string `json:"context_id"`
	After         int64  `json:"after,omitempty"`
	MaxBytes      int    `json:"max_bytes,omitempty"`
	Page          bool   `json:"page,omitempty"`
	AgentInstance string `json:"agent_instance,omitempty"`
	ForInstance   string `json:"for_instance,omitempty"`
	FromInstance  string `json:"from_instance,omitempty"`
	TaskID        string `json:"task_id,omitempty"`
	Schema        string `json:"schema,omitempty"`
	Limit         *int   `json:"limit,omitempty"`
}

type waitRequest struct {
	ContextID      string   `json:"context_id"`
	After          int64    `json:"after,omitempty"`
	MaxBytes       int      `json:"max_bytes,omitempty"`
	Page           bool     `json:"page,omitempty"`
	AgentInstance  string   `json:"agent_instance,omitempty"`
	ForInstance    string   `json:"for_instance,omitempty"`
	FromInstance   string   `json:"from_instance,omitempty"`
	TaskID         string   `json:"task_id,omitempty"`
	Schema         string   `json:"schema,omitempty"`
	TimeoutSeconds *float64 `json:"timeout_seconds,omitempty"`
}

type principalResult struct {
	Principal *principalDTO `json:"principal"`
}
type credentialResult struct {
	Credential *credentialDTO `json:"credential"`
}
type workspaceResult struct {
	Workspace *workspaceDTO `json:"workspace"`
}
type contextResult struct {
	Context *contextDTO `json:"context"`
}
type referenceResult struct {
	Reference *referenceDTO `json:"reference"`
}
type grantResult struct {
	Grant *grantDTO `json:"grant"`
}
type messageResult struct {
	Message *messageDTO `json:"message"`
}
type taskResult struct {
	Task *taskDTO `json:"task"`
}
type claimResult struct {
	Claim *claimDTO `json:"claim"`
}
type cursorResult struct {
	Cursor *int64 `json:"cursor"`
}
type serverInfoResult struct {
	ServerInfo *comitia.ServerInfo `json:"server_info"`
}
type storageStatusResult struct {
	StorageStatus *comitia.StorageStatus `json:"storage_status"`
}
type contextsResult struct {
	Contexts []contextDTO `json:"contexts"`
}
type participantsResult struct {
	Participants []participantDTO `json:"participants"`
}
type messagesResult struct {
	Messages []messageDTO `json:"messages"`
}
type tasksResult struct {
	Tasks []taskDTO `json:"tasks"`
}
type instancesResult struct {
	Instances []comitia.InstancePresence `json:"instances"`
}
type delegateResult struct {
	Principal  *principalDTO  `json:"principal"`
	Credential *credentialDTO `json:"credential"`
	Grants     []grantDTO     `json:"grants"`
}
type listTasksResult struct {
	Tasks    *[]taskDTO   `json:"tasks,omitempty"`
	TaskPage *taskPageDTO `json:"task_page,omitempty"`
}
type listParticipantsResult struct {
	Participants    *[]participantDTO   `json:"participants,omitempty"`
	ParticipantPage *participantPageDTO `json:"participant_page,omitempty"`
}
type readMessagesResult struct {
	Messages *[]messageDTO `json:"messages,omitempty"`
	Page     *pageDTO      `json:"page,omitempty"`
}

func ptr[T any](v T) *T { return &v }
func mapSlice[A, B any](in []A, convert func(A) B) []B {
	out := make([]B, len(in))
	for i, v := range in {
		out[i] = convert(v)
	}
	return out
}
func wireCredential(c comitia.Credential) credentialDTO {
	return credentialDTO{sessionDTO(c.Session), c.Token}
}
func (c credentialDTO) core() comitia.Credential {
	return comitia.Credential{Session: comitia.Session(c.Session), Token: c.Token}
}
func wireClaim(c comitia.TaskClaim) claimDTO { return claimDTO{taskDTO(c.Task), c.Token} }
func (c claimDTO) core() comitia.TaskClaim {
	return comitia.TaskClaim{Task: comitia.Task(c.Task), Token: c.Token}
}
func wireTaskPage(p comitia.TaskPage) taskPageDTO {
	return taskPageDTO{mapSlice(p.Tasks, func(t comitia.Task) taskDTO { return taskDTO(t) }), p.NextAfter, p.HasMore}
}
func (p taskPageDTO) core() comitia.TaskPage {
	return comitia.TaskPage{Tasks: mapSlice(p.Tasks, func(t taskDTO) comitia.Task { return comitia.Task(t) }), NextAfter: p.NextAfter, HasMore: p.HasMore}
}
func wireParticipantPage(p comitia.ParticipantPage) participantPageDTO {
	return participantPageDTO{mapSlice(p.Participants, func(v comitia.Participant) participantDTO { return participantDTO(v) }), p.NextAfter, p.HasMore}
}
func (p participantPageDTO) core() comitia.ParticipantPage {
	return comitia.ParticipantPage{Participants: mapSlice(p.Participants, func(v participantDTO) comitia.Participant { return comitia.Participant(v) }), NextAfter: p.NextAfter, HasMore: p.HasMore}
}
func wirePage(p comitia.MessagePage) pageDTO {
	return pageDTO{mapSlice(p.Messages, func(m comitia.Message) messageDTO { return messageDTO(m) }), p.NextAfter, p.ScannedThrough, p.HasMore}
}
func (p pageDTO) core() comitia.MessagePage {
	// Servers before scanned_through only reported delivered progress.
	return comitia.MessagePage{Messages: mapSlice(p.Messages, func(m messageDTO) comitia.Message { return comitia.Message(m) }), NextAfter: p.NextAfter, ScannedThrough: max(p.ScannedThrough, p.NextAfter), HasMore: p.HasMore}
}
