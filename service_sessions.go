package comitia

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

func (s *Service) Bootstrap(ctx context.Context, in BootstrapInput) (Principal, Credential, error) {
	var p Principal
	var c Credential
	if s.bootstrapCredential == "" || subtle.ConstantTimeCompare([]byte(in.Credential), []byte(s.bootstrapCredential)) != 1 {
		return p, c, ErrUnauthenticated
	}
	if strings.TrimSpace(in.AdminName) == "" || in.SessionTTL < minSessionTTL {
		return p, c, Invalidf("admin_name required and session_ttl must be at least 1s")
	}
	err := s.store.Update(ctx, func(w WriteStore) error {
		used, e := w.BootstrapDone()
		if e != nil {
			return e
		}
		if used {
			return ErrBootstrapUsed
		}
		now := s.now().UTC()
		p = Principal{ID: model.NewID(), Name: in.AdminName, Admin: true, CreatedAt: now}
		raw := token()
		c = Credential{Token: raw, Session: Session{ID: model.NewID(), PrincipalID: p.ID, ExpiresAt: now.Add(in.SessionTTL), CreatedAt: now}}
		if e = w.PutPrincipal(p); e != nil {
			return e
		}
		if e = w.PutSession(c.Session, tokenHash(raw)); e != nil {
			return e
		}
		return w.MarkBootstrapDone()
	})
	return p, c, err
}

func (s *Service) CreatePrincipal(ctx context.Context, raw, name string) (Principal, error) {
	var p Principal
	if strings.TrimSpace(name) == "" {
		return p, Invalidf("name required")
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		if _, e := s.admin(w, raw); e != nil {
			return e
		}
		p = Principal{ID: model.NewID(), Name: name, CreatedAt: s.now().UTC()}
		return w.PutPrincipal(p)
	})
	return p, e
}

func (s *Service) MintSession(ctx context.Context, raw, principalID string, ttl time.Duration) (Credential, error) {
	var c Credential
	if e := validSessionTTL(ttl); e != nil {
		return c, e
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		if _, e := s.admin(w, raw); e != nil {
			return e
		}
		if _, e := w.Principal(principalID); e != nil {
			return e
		}
		now := s.now().UTC()
		t := token()
		c = Credential{Token: t, Session: Session{ID: model.NewID(), PrincipalID: principalID, ExpiresAt: now.Add(ttl), CreatedAt: now}}
		return w.PutSession(c.Session, tokenHash(t))
	})
	return c, e
}

func (s *Service) DelegateSession(ctx context.Context, raw string, in DelegateInput) (Principal, Credential, []Grant, error) {
	var p Principal
	var c Credential
	var out []Grant
	if strings.TrimSpace(in.PrincipalName) == "" || in.TTL < minSessionTTL || len(in.Grants) == 0 {
		return p, c, nil, Invalidf("principal_name required, ttl must be at least 1s and grants must not be empty")
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		parent, e := s.auth(w, raw)
		if e != nil {
			return e
		}
		now := s.now().UTC()
		expiry := now.Add(in.TTL)
		if expiry.After(parent.session.ExpiresAt) {
			return ErrForbidden
		}
		if e = checkDelegationDepth("session", parent.session.ID, sessionParent(w)); e != nil {
			return e
		}
		p = Principal{ID: model.NewID(), Name: in.PrincipalName, CreatedAt: now}
		tok := token()
		c = Credential{Token: tok, Session: Session{ID: model.NewID(), PrincipalID: p.ID, ParentID: parent.session.ID, ExpiresAt: expiry, CreatedAt: now}}
		for _, req := range in.Grants {
			if req.Permissions == 0 || req.Permissions&^PermissionAll != 0 || req.ExpiresAt.After(expiry) || !req.ExpiresAt.After(now) {
				return Invalidf("grants permissions must be nonzero known bits and expires_at must be within session expiry")
			}
			_, parents, e := s.require(w, raw, req.ContextID, PermissionGrant)
			if e != nil {
				return e
			}
			if e = s.contextActive(w, req.ContextID); e != nil {
				return e
			}
			source, e := delegationSource(w, parents, req.Permissions, req.ExpiresAt)
			if e != nil {
				return e
			}
			out = append(out, Grant{ID: model.NewID(), SessionID: c.Session.ID, ContextID: req.ContextID, ParentGrantID: source.ID, IssuerSessionID: parent.session.ID, Permissions: req.Permissions, ExpiresAt: req.ExpiresAt, CreatedAt: now})
		}
		if e = w.PutPrincipal(p); e != nil {
			return e
		}
		if e = w.PutSession(c.Session, tokenHash(tok)); e != nil {
			return e
		}
		for _, g := range out {
			if e = w.PutGrant(g); e != nil {
				return e
			}
		}
		return nil
	})
	return p, c, out, e
}

func (s *Service) RevokeSession(ctx context.Context, raw, target string) error {
	return s.store.Update(ctx, func(w WriteStore) error {
		a, e := s.auth(w, raw)
		if e != nil {
			return e
		}
		t, e := w.Session(target)
		if !a.principal.Admin && errors.Is(e, ErrNotFound) {
			return ErrForbidden
		}
		if e != nil {
			return e
		}
		if !a.principal.Admin && t.ParentID != a.session.ID && t.ID != a.session.ID {
			return ErrForbidden
		}
		return revokedAgain(w.RevokeSessionTree(target, s.now().UTC()), t.RevokedAt)
	})
}

// Revocation is idempotent once authorized. Older stores report an already
// revoked root as missing, so trust the record read in this transaction.
func revokedAgain(err error, revokedAt *time.Time) error {
	if revokedAt != nil && errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
