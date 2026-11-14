package comitia

import (
	"database/sql"
	"strings"
	"time"
)

func contextIDArgs(ids []string) (string, []any, error) {
	if len(ids) > 1000 {
		return "", nil, Invalidf("context batch exceeds 1000 IDs")
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(ids)), ","), args, nil
}

func (x *sqlStore) GrantsForContexts(sid string, ids []string, now time.Time) (map[string][]Grant, error) {
	out := map[string][]Grant{}
	for _, id := range ids {
		out[id] = nil
	}
	if len(ids) == 0 {
		return out, nil
	}
	marks, args, err := contextIDArgs(ids)
	if err != nil {
		return nil, err
	}
	args = append([]any{sid}, args...)
	candidates, err := x.rawGrants("SELECT id,session_id,context_id,parent_grant_id,issuer_session_id,permissions,expires_at,revoked_at,created_at FROM grants WHERE session_id=? AND context_id IN ("+marks+")", args...)
	if err != nil {
		return nil, err
	}
	grants, err := x.effectiveGrants(candidates, now)
	if err != nil {
		return nil, err
	}
	for _, g := range grants {
		out[g.ContextID] = append(out[g.ContextID], g)
	}
	return out, nil
}

func (x *sqlStore) ContextArchiveTimes(ids []string, now time.Time) (map[string]*time.Time, error) {
	out := map[string]*time.Time{}
	if len(ids) == 0 {
		return out, nil
	}
	marks, args, err := contextIDArgs(ids)
	if err != nil {
		return nil, err
	}
	rows, err := x.q.Query(`SELECT c.id,a.archived_at,cr.creator_grant_id FROM contexts c
LEFT JOIN context_archives a ON a.context_id=c.id LEFT JOIN context_creators cr ON cr.context_id=c.id
WHERE c.id IN (`+marks+")", args...)
	if err != nil {
		return nil, err
	}
	type metadata struct {
		id                string
		explicit, creator sql.NullString
	}
	var records []metadata
	for rows.Next() {
		var m metadata
		if err := rows.Scan(&m.id, &m.explicit, &m.creator); err != nil {
			rows.Close()
			return nil, err
		}
		records = append(records, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	policy := x.grantPolicy()
	var missing []string
	seen := map[string]bool{}
	for _, m := range records {
		if m.creator.Valid {
			if _, ok := knownGrant(policy, m.creator.String); !ok && !seen[m.creator.String] {
				missing = append(missing, m.creator.String)
				seen[m.creator.String] = true
			}
		}
	}
	if len(missing) > 0 {
		marks, args, err := contextIDArgs(missing)
		if err != nil {
			return nil, err
		}
		grants, err := x.rawGrants("SELECT id,session_id,context_id,parent_grant_id,issuer_session_id,permissions,expires_at,revoked_at,created_at FROM grants WHERE id IN ("+marks+")", args...)
		if err != nil {
			return nil, err
		}
		for _, g := range grants {
			rememberGrant(policy, g)
		}
	}
	for _, m := range records {
		at, err := x.contextArchiveTime(m.explicit, m.creator, now)
		if err != nil {
			return nil, err
		}
		out[m.id] = at
	}
	for _, id := range ids {
		if _, ok := out[id]; !ok {
			return nil, ErrNotFound
		}
	}
	return out, nil
}
