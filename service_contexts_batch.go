package comitia

import (
	"time"
)

type contextGrantBatch struct {
	ReadStore
	sessionID string
	grants    map[string][]Grant
	now       time.Time
	contexts  map[string]WorkContext
}

func (r *contextGrantBatch) Grants(sid, cid string, now time.Time) ([]Grant, error) {
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

func (r *contextGrantBatch) Context(id string) (WorkContext, error) {
	if c, ok := r.contexts[id]; ok {
		return c, nil
	}
	c, err := r.ReadStore.Context(id)
	if err == nil {
		r.contexts[id] = c
	}
	return c, err
}

func contextGrants(r ReadStore, sid string, contexts []WorkContext, now time.Time) (ReadStore, error) {
	gs := map[string][]Grant{}
	if batch, ok := r.(BatchContextReadStore); ok {
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
		gs = map[string][]Grant{}
	}
	cs := map[string]WorkContext{}
	for _, c := range contexts {
		cs[c.ID] = c
	}
	return &contextGrantBatch{ReadStore: r, sessionID: sid, grants: gs, now: now, contexts: cs}, nil
}

func (s *Service) contextMetadataBatch(r ReadStore, contexts []WorkContext, now time.Time) error {
	if len(contexts) == 0 {
		return nil
	}
	if batch, ok := r.(BatchContextReadStore); ok {
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
