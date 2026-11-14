package service

import (
	"time"

	"github.com/krimvp/comitia/internal/model"
)

type contextGrantBatch struct {
	model.ReadStore
	sessionID string
	grants    map[string][]model.Grant
	now       time.Time
	contexts  map[string]model.WorkContext
}

func (r *contextGrantBatch) Grants(sid, cid string, now time.Time) ([]model.Grant, error) {
	if sid != r.sessionID || !now.Equal(r.now) {
		return r.ReadStore.Grants(sid, cid, now)
	}
	if gs, ok := r.grants[cid]; ok {
		return gs, nil
	}
	gs, err := r.ReadStore.Grants(sid, cid, now)
	if err == nil {
		r.grants[cid] = gs
	}
	return gs, err
}

func (r *contextGrantBatch) Context(id string) (model.WorkContext, error) {
	if c, ok := r.contexts[id]; ok {
		return c, nil
	}
	c, err := r.ReadStore.Context(id)
	if err == nil {
		r.contexts[id] = c
	}
	return c, err
}

func contextGrants(r model.ReadStore, sid string, contexts []model.WorkContext, now time.Time) (model.ReadStore, error) {
	gs := map[string][]model.Grant{}
	if batch, ok := r.(model.BatchContextReadStore); ok {
		ids := make([]string, len(contexts))
		for i, c := range contexts {
			ids[i] = c.ID
		}
		var err error
		gs, err = batch.GrantsForContexts(sid, ids, now)
		if err != nil {
			return nil, err
		}
	}
	if gs == nil {
		gs = map[string][]model.Grant{}
	}
	cs := map[string]model.WorkContext{}
	for _, c := range contexts {
		cs[c.ID] = c
	}
	return &contextGrantBatch{ReadStore: r, sessionID: sid, grants: gs, now: now, contexts: cs}, nil
}

func (s *Service) contextMetadataBatch(r model.ReadStore, contexts []model.WorkContext, now time.Time) error {
	if len(contexts) == 0 {
		return nil
	}
	if batch, ok := r.(model.BatchContextReadStore); ok {
		ids := make([]string, len(contexts))
		for i, c := range contexts {
			ids[i] = c.ID
		}
		times, err := batch.ContextArchiveTimes(ids, now)
		if err != nil {
			return err
		}
		for i := range contexts {
			at := times[contexts[i].ID]
			contexts[i].ArchivedAt, contexts[i].Archived = at, at != nil
		}
		return nil
	}
	for i := range contexts {
		if err := s.contextMetadataAt(r, &contexts[i], now); err != nil {
			return err
		}
	}
	return nil
}
