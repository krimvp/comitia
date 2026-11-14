package comitia

import (
	"database/sql"
	"time"
)

const contextLifecycleSchema = `
CREATE TABLE IF NOT EXISTS context_archives (context_id TEXT PRIMARY KEY REFERENCES contexts(id), archived_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS context_creators (context_id TEXT PRIMARY KEY REFERENCES contexts(id), creator_grant_id TEXT NOT NULL REFERENCES grants(id), creating_grant_id TEXT NOT NULL REFERENCES grants(id));
CREATE INDEX IF NOT EXISTS context_creators_grant ON context_creators(creating_grant_id);
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
	return x.contextArchiveTime(explicit, creator, now)
}

func (x *sqlStore) contextArchiveTime(explicit, creator sql.NullString, now time.Time) (*time.Time, error) {
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

func (x *sqlStore) creatorGrantEnd(grantID string) (time.Time, error) {
	return x.grantPolicy().End(grantID)
}
