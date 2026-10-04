package comitia

import (
	"database/sql"
	"fmt"
	"time"
)

const contextLifecycleSchema = `
CREATE TABLE IF NOT EXISTS context_archives (context_id TEXT PRIMARY KEY REFERENCES contexts(id), archived_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS context_creators (context_id TEXT PRIMARY KEY REFERENCES contexts(id), creator_grant_id TEXT NOT NULL REFERENCES grants(id), creating_grant_id TEXT NOT NULL REFERENCES grants(id));
CREATE INDEX IF NOT EXISTS context_creators_grant ON context_creators(creating_grant_id);
INSERT OR IGNORE INTO context_creators
SELECT p.context_id,g.id,g.parent_grant_id FROM context_parents p JOIN grants g ON g.id=(
 SELECT g2.id FROM grants g2 JOIN grants source ON source.id=g2.parent_grant_id
 WHERE g2.context_id=p.context_id AND g2.session_id=p.creator_session_id AND g2.issuer_session_id=p.creator_session_id
 AND source.context_id=p.parent_context_id ORDER BY g2.created_at,g2.id LIMIT 1
);
`

func (x *sqlStore) PutContextCreator(contextID, creatorGrantID, creatingGrantID string) error {
	_, err := x.q.Exec("INSERT INTO context_creators VALUES(?,?,?)", contextID, creatorGrantID, creatingGrantID)
	return err
}

func (x *sqlStore) ChildContextCount(grantID string) (int, error) {
	var count int
	err := x.q.QueryRow("SELECT COUNT(*) FROM context_creators WHERE creating_grant_id=?", grantID).Scan(&count)
	return count, err
}

func (x *sqlStore) PutContextArchive(contextID string, at time.Time) error {
	_, err := x.q.Exec("INSERT OR IGNORE INTO context_archives VALUES(?,?)", contextID, ts(at))
	return err
}

func (x *sqlStore) ContextArchivedAt(contextID string, now time.Time) (*time.Time, error) {
	var explicit, creator sql.NullString
	err := x.q.QueryRow(`SELECT a.archived_at,cr.creator_grant_id FROM contexts c
LEFT JOIN context_archives a ON a.context_id=c.id LEFT JOIN context_creators cr ON cr.context_id=c.id
WHERE c.id=?`, contextID).Scan(&explicit, &creator)
	if err != nil {
		return nil, dbErr(err)
	}
	at := nullTime(explicit)
	if creator.Valid {
		end, err := x.creatorGrantEnd(creator.String)
		if err != nil {
			return nil, err
		}
		if !end.After(now) && (at == nil || end.Before(*at)) {
			at = &end
		}
	}
	return at, nil
}

// Match Grants' effective ancestry checks, retaining the first instant access ended.
func (x *sqlStore) creatorGrantEnd(grantID string) (time.Time, error) {
	var end time.Time
	include := func(at time.Time) {
		if end.IsZero() || at.Before(end) {
			end = at
		}
	}
	seen := map[string]bool{}
	for grantID != "" {
		if seen[grantID] {
			return end, fmt.Errorf("%w: creator grant cycle", ErrInvalid)
		}
		seen[grantID] = true
		g, err := x.grant(grantID)
		if err != nil {
			return end, err
		}
		include(g.ExpiresAt)
		if g.RevokedAt != nil {
			include(*g.RevokedAt)
		}
		for _, sessionID := range []string{g.SessionID, g.IssuerSessionID} {
			ss, err := x.Session(sessionID)
			if err != nil {
				return end, err
			}
			include(ss.ExpiresAt)
			if ss.RevokedAt != nil {
				include(*ss.RevokedAt)
			}
		}
		grantID = g.ParentGrantID
	}
	return end, nil
}
