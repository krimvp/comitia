package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
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
const instanceHeartbeatBudget = 25 * time.Millisecond

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
type instanceLabelStore interface {
	InstanceLabel(string, string, string) (string, error)
}
type PresenceWriteStore interface {
	PutInstancePresence(InstancePresence) error
	// A failed heartbeat must not undo or poison the caller's transaction.
	TouchInstancePresence(InstancePresence) error
}

func (s *Service) ListInstances(ctx context.Context, raw, cid string) ([]InstancePresence, error) {
	page, err := s.instancePage(ctx, raw, cid, "", DefaultInstanceListLimit, false)
	return page.Instances, err
}

func (s *Service) ListInstancesPage(ctx context.Context, raw, cid, after string, limit int) (InstancePage, error) {
	return s.instancePage(ctx, raw, cid, after, limit, true)
}

func (s *Service) instancePage(ctx context.Context, raw, cid, after string, limit int, paged bool) (InstancePage, error) {
	page := InstancePage{Instances: []InstancePresence{}, NextAfter: after}
	sid, iid, err := decodeInstanceCursor(after, limit)
	if err != nil {
		return InstancePage{}, err
	}
	err = s.store.View(ctx, func(r ReadStore) error {
		if _, _, err := s.require(r, raw, cid, PermissionRead); err != nil {
			return err
		}
		now := s.now().UTC()
		if pages, ok := r.(InstancePageStore); ok {
			rows, err := pages.InstancesAfter(cid, now, sid, iid, limit+1)
			if err != nil {
				return err
			}
			page.HasMore = len(rows) > limit
			if page.HasMore {
				rows = rows[:limit]
			}
			if len(rows) > 0 {
				page.Instances = rows
				page.NextAfter = instanceCursor(rows[len(rows)-1])
			}
		} else {
			rows, err := r.Instances(cid, now)
			if err != nil {
				return err
			}
			if !paged && len(rows) > limit {
				rows = rows[:limit]
			}
			if paged {
				if page, err = legacyInstancePage(rows, after, limit, 1<<62, "store"); err != nil {
					return err
				}
			} else if rows != nil {
				page.Instances = rows
			}
		}
		annotateInstances(page.Instances, now)
		return nil
	})
	if err != nil {
		return InstancePage{}, err
	}
	return page, nil
}

func annotateInstances(rows []InstancePresence, now time.Time) {
	for i := range rows {
		if rows[i].LastSeen.IsZero() {
			continue
		}
		age := now.Sub(rows[i].LastSeen)
		if age < 0 {
			age = 0
		}
		seconds, recent := int64(age/time.Second), age < InstanceRecentWindow
		rows[i].SeenSecondsAgo, rows[i].Recent = &seconds, &recent
	}
}

func instanceCursor(p InstancePresence) string { return p.SessionID + "/" + p.ID }
func decodeInstanceCursor(after string, limit int) (string, string, error) {
	if limit < 1 || limit > 1000 {
		return "", "", Invalidf("limit must be between 1 and 1000")
	}
	if after == "" {
		return "", "", nil
	}
	i := strings.LastIndexByte(after, '/')
	if i < 1 || len(after) > 256 || !validInstanceID(after[i+1:]) {
		return "", "", Invalidf("invalid instance cursor")
	}
	return after[:i], after[i+1:], nil
}

// Legacy lists have no continuation; at the ceiling, reject paging rather than hide a tail.
func legacyInstancePage(rows []InstancePresence, after string, limit, ceiling int, source string) (InstancePage, error) {
	if len(rows) >= ceiling {
		return InstancePage{}, Invalidf("instance pagination beyond the legacy limit is unsupported by this %s", source)
	}
	sid, iid, err := decodeInstanceCursor(after, limit)
	if err != nil {
		return InstancePage{}, err
	}
	rows = append([]InstancePresence{}, rows...)
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].SessionID < rows[j].SessionID || rows[i].SessionID == rows[j].SessionID && rows[i].ID < rows[j].ID
	})
	page := InstancePage{Instances: []InstancePresence{}, NextAfter: after}
	for _, row := range rows {
		if after != "" && (row.SessionID < sid || row.SessionID == sid && row.ID <= iid) {
			continue
		}
		if len(page.Instances) == limit {
			page.HasMore = true
			break
		}
		page.Instances = append(page.Instances, row)
		page.NextAfter = instanceCursor(row)
	}
	return page, nil
}

// ListInstancePage pages APIs that predate instance paging from their
// bounded unpaged list.
func ListInstancePage(ctx context.Context, api PresenceAPI, token, cid, after string, limit int) (InstancePage, error) {
	if pages, ok := api.(InstancePageAPI); ok {
		return pages.ListInstancesPage(ctx, token, cid, after, limit)
	}
	if _, _, err := decodeInstanceCursor(after, limit); err != nil {
		return InstancePage{}, err
	}
	rows, err := api.ListInstances(ctx, token, cid)
	if err != nil {
		return InstancePage{}, err
	}
	return legacyInstancePage(rows, after, limit, DefaultInstanceListLimit, "API")
}

func (s *Service) SetInstanceResume(ctx context.Context, raw, cid string, in *AgentInstance, resume json.RawMessage) error {
	instance, err := normalizeAgentInstance(in)
	if err != nil {
		return err
	}
	if instance == nil {
		return Invalidf("agent_instance required")
	}
	var object map[string]json.RawMessage
	if len(resume) > MaxInstanceResumeBytes || json.Unmarshal(resume, &object) != nil || object == nil {
		return Invalidf("resume must be a JSON object of at most %d bytes", MaxInstanceResumeBytes)
	}
	return s.store.Update(ctx, func(w WriteStore) error {
		a, _, err := s.require(w, raw, cid, PermissionRead)
		if err != nil {
			return err
		}
		if err := s.contextActive(w, cid); err != nil {
			return err
		}
		return w.PutInstancePresence(InstancePresence{ID: instance.ID, Label: instance.Label, SessionID: a.session.ID, ContextID: cid, LastSeen: s.now().UTC(), Resume: resume})
	})
}

// Polls check freshness inside their existing View before opening a write.
func (s *Service) instanceHeartbeatDue(r ReadStore, sid, cid, instance string) bool {
	if instance == "" {
		return false
	}
	seen, err := r.InstanceLastSeen(sid, cid, instance)
	return errors.Is(err, ErrNotFound) || err == nil && !seen.After(s.now().UTC().Add(-InstanceHeartbeatInterval))
}

// Message and task writes reuse their transaction; SQLite isolates the heartbeat with a savepoint.
func (s *Service) touchInstanceIn(w WriteStore, sid, cid string, instance *AgentInstance) {
	if instance == nil || instance.ID == "" {
		return
	}
	due := s.instanceHeartbeatDue(w, sid, cid, instance.ID)
	if !due && instance.Label != "" {
		label, err := s.instanceLabel(w, sid, cid, instance.ID)
		due = err == nil && label != instance.Label
	}
	if !due {
		return
	}
	_ = w.TouchInstancePresence(InstancePresence{ID: instance.ID, Label: instance.Label, SessionID: sid, ContextID: cid, LastSeen: s.now().UTC()})
}

func (s *Service) instanceLabel(r ReadStore, sid, cid, instance string) (string, error) {
	if labels, ok := r.(instanceLabelStore); ok {
		return labels.InstanceLabel(sid, cid, instance)
	}
	rows, err := r.Instances(cid, s.now().UTC())
	if err != nil {
		return "", err
	}
	for _, p := range rows {
		if p.SessionID == sid && p.ID == instance {
			return p.Label, nil
		}
	}
	return "", ErrNotFound
}

func (s *Service) touchInstance(ctx context.Context, sid, cid string, instance *AgentInstance) {
	ctx, cancel := context.WithTimeout(ctx, instanceHeartbeatBudget)
	defer cancel()
	_ = s.store.Update(ctx, func(w WriteStore) error {
		// Another reader may have recorded activity since the original View.
		s.touchInstanceIn(w, sid, cid, instance)
		return nil
	})
}
