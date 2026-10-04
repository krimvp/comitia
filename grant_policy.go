package comitia

import (
	"errors"
	"fmt"
	"time"
)

// GrantLookup supplies raw records, including expired and revoked ones.
type GrantLookup interface {
	Grant(string) (Grant, error)
	Session(string) (Session, error)
}

// GrantEvaluator shares ancestry facts within one transaction. Discard it after
// any grant/session mutation and at transaction end. It is not concurrency safe.
// Session parent links are not grant ancestry: RevokeSessionTree must revoke
// descendants durably. Child-context grants may derive from another context.
type GrantEvaluator struct {
	lookup   GrantLookup
	grants   map[string]Grant
	sessions map[string]grantLifetime
	ends     map[string]grantLifetime
}
type grantLifetime struct {
	end     time.Time
	revoked bool
	err     error
}

func NewGrantEvaluator(lookup GrantLookup) *GrantEvaluator {
	return &GrantEvaluator{lookup: lookup, grants: map[string]Grant{}, sessions: map[string]grantLifetime{}, ends: map[string]grantLifetime{}}
}
func lifetime(expires time.Time, revoked *time.Time) grantLifetime {
	v := grantLifetime{end: expires, revoked: revoked != nil}
	if revoked != nil && revoked.Before(v.end) {
		v.end = *revoked
	}
	return v
}
func mergeLifetime(a, b grantLifetime) grantLifetime {
	if b.end.Before(a.end) {
		a.end = b.end
	}
	a.revoked = a.revoked || b.revoked
	// Backend failures take precedence over missing or invalid ancestry.
	if b.err != nil && (a.err == nil || errors.Is(a.err, ErrNotFound) || errors.Is(a.err, ErrInvalid)) {
		a.err = b.err
	}
	return a
}
func (p *GrantEvaluator) session(id string) grantLifetime {
	if v, ok := p.sessions[id]; ok {
		return v
	}
	s, err := p.lookup.Session(id)
	v := lifetime(s.ExpiresAt, s.RevokedAt)
	v.err = err
	p.sessions[id] = v
	return v
}
func (p *GrantEvaluator) evaluate(id string) grantLifetime {
	if v, ok := p.ends[id]; ok {
		return v
	}
	var path []string
	var values []grantLifetime
	seen := map[string]bool{}
	var result grantLifetime
	for {
		if v, ok := p.ends[id]; ok {
			result = v
			break
		}
		if seen[id] {
			result.err = fmt.Errorf("%w: grant ancestry cycle", ErrInvalid)
			break
		}
		seen[id] = true
		g, ok := p.grants[id]
		if !ok {
			var err error
			g, err = p.lookup.Grant(id)
			if err != nil {
				result.err = err
				break
			}
			p.grants[id] = g
		}
		v := mergeLifetime(lifetime(g.ExpiresAt, g.RevokedAt), p.session(g.SessionID))
		v = mergeLifetime(v, p.session(g.IssuerSessionID))
		path = append(path, id)
		values = append(values, v)
		if v.err != nil || g.ParentGrantID == "" {
			result = v
			break
		}
		id = g.ParentGrantID
	}
	for i := len(path) - 1; i >= 0; i-- {
		result = mergeLifetime(values[i], result)
		p.ends[path[i]] = result
	}
	return result
}

// Effective rejects a grant if it or any grant ancestor, owner or issuer is
// revoked, expires at/before now, is missing, or belongs to a cyclic chain.
// Unexpected lookup failures are returned rather than treated as authorization.
func (p *GrantEvaluator) Effective(g Grant, now time.Time) (bool, error) {
	p.grants[g.ID] = g
	v := p.evaluate(g.ID)
	if errors.Is(v.err, ErrNotFound) || errors.Is(v.err, ErrInvalid) {
		return false, nil
	}
	return v.err == nil && !v.revoked && v.end.After(now), v.err
}

// End returns the first expiry/revocation instant in the same ancestry used by
// Effective. Missing ancestry and cycles return ErrNotFound and ErrInvalid.
// Context archival uses this instant even when an independent read grant exists.
func (p *GrantEvaluator) End(grantID string) (time.Time, error) {
	v := p.evaluate(grantID)
	return v.end, v.err
}

func (x *sqlStore) grantPolicy() *GrantEvaluator {
	if x.grantEvaluator == nil {
		x.grantEvaluator = NewGrantEvaluator(x)
	}
	return x.grantEvaluator
}
func (x *sqlStore) invalidateGrantPolicy() { x.grantEvaluator = nil }
