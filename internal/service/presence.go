package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

const instanceHeartbeatBudget = 25 * time.Millisecond

type instanceLabelStore interface {
	InstanceLabel(string, string, string) (string, error)
}

func (s *Service) ListInstances(ctx context.Context, raw, cid string) ([]model.InstancePresence, error) {
	page, err := s.instancePage(ctx, raw, cid, "", model.DefaultInstanceListLimit, false)
	return page.Instances, err
}

func (s *Service) ListInstancesPage(ctx context.Context, raw, cid, after string, limit int) (model.InstancePage, error) {
	return s.instancePage(ctx, raw, cid, after, limit, true)
}

func (s *Service) instancePage(ctx context.Context, raw, cid, after string, limit int, paged bool) (model.InstancePage, error) {
	page := model.InstancePage{Instances: []model.InstancePresence{}, NextAfter: after}
	sid, iid, err := decodeInstanceCursor(after, limit)
	if err != nil {
		return model.InstancePage{}, err
	}
	err = s.store.View(ctx, func(r model.ReadStore) error {
		if _, _, err := s.require(r, raw, cid, model.PermissionRead); err != nil {
			return err
		}
		now := s.now().UTC()
		if pages, ok := r.(model.InstancePageStore); ok {
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
		return model.InstancePage{}, err
	}
	return page, nil
}

func annotateInstances(rows []model.InstancePresence, now time.Time) {
	for i := range rows {
		if rows[i].LastSeen.IsZero() {
			continue
		}
		age := now.Sub(rows[i].LastSeen)
		if age < 0 {
			age = 0
		}
		seconds, recent := int64(age/time.Second), age < model.InstanceRecentWindow
		rows[i].SeenSecondsAgo, rows[i].Recent = &seconds, &recent
	}
}

func instanceCursor(p model.InstancePresence) string { return p.SessionID + "/" + p.ID }

func decodeInstanceCursor(after string, limit int) (string, string, error) {
	if limit < 1 || limit > model.MaxListLimit {
		return "", "", model.Invalidf("limit must be between 1 and %d", model.MaxListLimit)
	}
	if after == "" {
		return "", "", nil
	}
	i := strings.LastIndexByte(after, '/')
	if i < 1 || len(after) > 256 || !model.ValidInstanceID(after[i+1:]) {
		return "", "", model.Invalidf("invalid instance cursor")
	}
	return after[:i], after[i+1:], nil
}

// Legacy lists have no continuation; at the ceiling, reject paging rather than hide a tail.
func legacyInstancePage(rows []model.InstancePresence, after string, limit, ceiling int, source string) (model.InstancePage, error) {
	if len(rows) >= ceiling {
		return model.InstancePage{}, model.Invalidf("instance pagination beyond the legacy limit is unsupported by this %s", source)
	}
	sid, iid, err := decodeInstanceCursor(after, limit)
	if err != nil {
		return model.InstancePage{}, err
	}
	rows = append([]model.InstancePresence{}, rows...)
	sort.Slice(rows, func(i, j int) bool {
		return rows[i].SessionID < rows[j].SessionID || rows[i].SessionID == rows[j].SessionID && rows[i].ID < rows[j].ID
	})
	page := model.InstancePage{Instances: []model.InstancePresence{}, NextAfter: after}
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
func ListInstancePage(ctx context.Context, api model.PresenceAPI, token, cid, after string, limit int) (model.InstancePage, error) {
	if pages, ok := api.(model.InstancePageAPI); ok {
		return pages.ListInstancesPage(ctx, token, cid, after, limit)
	}
	if _, _, err := decodeInstanceCursor(after, limit); err != nil {
		return model.InstancePage{}, err
	}
	rows, err := api.ListInstances(ctx, token, cid)
	if err != nil {
		return model.InstancePage{}, err
	}
	return legacyInstancePage(rows, after, limit, model.DefaultInstanceListLimit, "API")
}

func (s *Service) SetInstanceResume(ctx context.Context, raw, cid string, in *model.AgentInstance, resume json.RawMessage) error {
	instance, err := model.NormalizeAgentInstance(in)
	if err != nil {
		return err
	}
	if instance == nil {
		return model.Invalidf("agent_instance required")
	}
	var object map[string]json.RawMessage
	if len(resume) > model.MaxInstanceResumeBytes || json.Unmarshal(resume, &object) != nil || object == nil {
		return model.Invalidf("resume must be a JSON object of at most %d bytes", model.MaxInstanceResumeBytes)
	}
	return s.store.Update(ctx, func(w model.WriteStore) error {
		a, _, err := s.require(w, raw, cid, model.PermissionRead)
		if err != nil {
			return err
		}
		if err := s.contextActive(w, cid); err != nil {
			return err
		}
		return w.PutInstancePresence(model.InstancePresence{ID: instance.ID, Label: instance.Label, SessionID: a.session.ID, ContextID: cid, LastSeen: s.now().UTC(), Resume: resume})
	})
}

// Polls check freshness inside their existing View before opening a write.
func (s *Service) instanceHeartbeatDue(r model.ReadStore, sid, cid, instance string) bool {
	if instance == "" {
		return false
	}
	seen, err := r.InstanceLastSeen(sid, cid, instance)
	return errors.Is(err, model.ErrNotFound) || err == nil && !seen.After(s.now().UTC().Add(-model.InstanceHeartbeatInterval))
}

// Message and task writes reuse their transaction; SQLite isolates the heartbeat with a savepoint.
func (s *Service) touchInstanceIn(w model.WriteStore, sid, cid string, instance *model.AgentInstance) {
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
	_ = w.TouchInstancePresence(model.InstancePresence{ID: instance.ID, Label: instance.Label, SessionID: sid, ContextID: cid, LastSeen: s.now().UTC()})
}

func (s *Service) instanceLabel(r model.ReadStore, sid, cid, instance string) (string, error) {
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
	return "", model.ErrNotFound
}

func (s *Service) touchInstance(ctx context.Context, sid, cid string, instance *model.AgentInstance) {
	budget := s.heartbeatBudget
	if budget == 0 {
		budget = instanceHeartbeatBudget
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	_ = s.store.Update(ctx, func(w model.WriteStore) error {
		// Another reader may have recorded activity since the original View.
		s.touchInstanceIn(w, sid, cid, instance)
		return nil
	})
}
