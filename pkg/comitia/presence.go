package comitia

import (
	"context"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/service"
)

// Agent instance presence.

const MaxInstanceResumeBytes = model.MaxInstanceResumeBytes

const // Stores keep at most this many instances per (session, context); a new
// instance evicts that session's least recently seen row, never another's.
MaxInstancesPerSession = model.MaxInstancesPerSession

const // Rows of revoked or expired sessions stay listed (inactive) for this long,
// then disappear from lists and are pruned by later presence writes.
InstanceRetention = model.InstanceRetention

const // Unpaged ListInstances returns at most this many rows.
DefaultInstanceListLimit = model.DefaultInstanceListLimit

const InstanceHeartbeatInterval = model.InstanceHeartbeatInterval

const InstanceRecentWindow = model.InstanceRecentWindow

// InstancePresence is self-reported process metadata, never proof of identity
// or liveness.
type InstancePresence = model.InstancePresence

// InstancePage cursors are exclusive "session_id/instance_id" pairs.
type InstancePage = model.InstancePage

// Paging is optional, like participant paging.
type InstancePageAPI = model.InstancePageAPI

type PresenceAPI = model.PresenceAPI

// ListInstancePage pages APIs that predate instance paging from their
// bounded unpaged list.
func ListInstancePage(ctx context.Context, api PresenceAPI, token, cid, after string, limit int) (InstancePage, error) {
	return service.ListInstancePage(ctx, api, token, cid, after, limit)
}
