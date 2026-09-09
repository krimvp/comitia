package comitia

import "time"

func (x *sqlStore) Participants(cid string, now time.Time, limit int) ([]Participant, error) {
	rows, err := x.q.Query("SELECT DISTINCT session_id FROM grants WHERE context_id=? ORDER BY session_id", cid)
	if err != nil { return nil, err }
	var ids []string
	for rows.Next() {
		var sid string
		if err = rows.Scan(&sid); err != nil { rows.Close(); return nil, err }
		ids = append(ids, sid)
	}
	err = rows.Err()
	rows.Close()
	if err != nil { return nil, err }
	out := []Participant{}
	for _, sid := range ids {
		session, err := x.Session(sid)
		if err != nil { return nil, err }
		if session.RevokedAt != nil || !session.ExpiresAt.After(now) { continue }
		grants, err := x.Grants(sid, cid, now)
		if err != nil { return nil, err }
		readable := false
		for _, g := range grants { if g.Permissions&PermissionRead != 0 { readable = true; break } }
		if !readable { continue }
		principal, err := x.Principal(session.PrincipalID)
		if err != nil { return nil, err }
		out = append(out, Participant{SessionID: sid, PrincipalID: principal.ID, Name: principal.Name, ExpiresAt: session.ExpiresAt})
		if len(out) == limit { break }
	}
	return out, nil
}
