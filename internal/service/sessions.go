package service

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

func (s *Service) Bootstrap(ctx context.Context, in model.BootstrapInput) (model.Principal, model.Credential, error) {
	var p model.Principal
	var c model.Credential
	if s.bootstrapCredential == "" || subtle.ConstantTimeCompare([]byte(in.Credential), []byte(s.bootstrapCredential)) != 1 {
		return p, c, model.ErrUnauthenticated
	}
	if strings.TrimSpace(in.AdminName) == "" || in.SessionTTL < minSessionTTL {
		return p, c, model.Invalidf("admin_name required and session_ttl must be at least 1s")
	}
	err := s.store.Update(ctx, func(w model.WriteStore) error {
		used, e := w.BootstrapDone()
		if e != nil {
			return e
		}
		if used {
			return model.ErrBootstrapUsed
		}
		now := s.now().UTC()
		p = model.Principal{ID: model.NewID(), Name: in.AdminName, Admin: true, CreatedAt: now}
		raw := token()
		c = model.Credential{Token: raw, Session: model.Session{ID: model.NewID(), PrincipalID: p.ID, ExpiresAt: now.Add(in.SessionTTL), CreatedAt: now}}
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

func (s *Service) CreatePrincipal(ctx context.Context, raw, name string) (model.Principal, error) {
	var p model.Principal
	if strings.TrimSpace(name) == "" {
		return p, model.Invalidf("name required")
	}
	e := s.store.Update(ctx, func(w model.WriteStore) error {
		if _, e := s.admin(w, raw); e != nil {
			return e
		}
		p = model.Principal{ID: model.NewID(), Name: name, CreatedAt: s.now().UTC()}
		return w.PutPrincipal(p)
	})
	return p, e
}

func (s *Service) MintSession(ctx context.Context, raw, principalID string, ttl time.Duration) (model.Credential, error) {
	var c model.Credential
	if e := validSessionTTL(ttl); e != nil {
		return c, e
	}
	e := s.store.Update(ctx, func(w model.WriteStore) error {
		if _, e := s.admin(w, raw); e != nil {
			return e
		}
		if _, e := w.Principal(principalID); e != nil {
			return e
		}
		now := s.now().UTC()
		t := token()
		c = model.Credential{Token: t, Session: model.Session{ID: model.NewID(), PrincipalID: principalID, ExpiresAt: now.Add(ttl), CreatedAt: now}}
		return w.PutSession(c.Session, tokenHash(t))
	})
	return c, e
}

func (s *Service) DelegateSession(ctx context.Context, raw string, in model.DelegateInput) (model.Principal, model.Credential, []model.Grant, error) {
	var p model.Principal
	var c model.Credential
	var out []model.Grant
	if strings.TrimSpace(in.PrincipalName) == "" || in.TTL < minSessionTTL || len(in.Grants) == 0 {
		return p, c, nil, model.Invalidf("principal_name required, ttl must be at least 1s and grants must not be empty")
	}
	e := s.store.Update(ctx, func(w model.WriteStore) error {
		parent, e := s.auth(w, raw)
		if e != nil {
			return e
		}
		now := s.now().UTC()
		expiry := now.Add(in.TTL)
		if expiry.After(parent.session.ExpiresAt) {
			return model.ErrForbidden
		}
		if e = checkDelegationDepth("session", parent.session.ID, sessionParent(w)); e != nil {
			return e
		}
		p = model.Principal{ID: model.NewID(), Name: in.PrincipalName, CreatedAt: now}
		tok := token()
		c = model.Credential{Token: tok, Session: model.Session{ID: model.NewID(), PrincipalID: p.ID, ParentID: parent.session.ID, ExpiresAt: expiry, CreatedAt: now}}
		for _, req := range in.Grants {
			if req.Permissions == 0 || req.Permissions&^model.PermissionAll != 0 || req.ExpiresAt.After(expiry) || !req.ExpiresAt.After(now) {
				return model.Invalidf("grants permissions must be nonzero known bits and expires_at must be within session expiry")
			}
			_, parents, e := s.require(w, raw, req.ContextID, model.PermissionGrant)
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
			out = append(out, model.Grant{ID: model.NewID(), SessionID: c.Session.ID, ContextID: req.ContextID, ParentGrantID: source.ID, IssuerSessionID: parent.session.ID, Permissions: req.Permissions, ExpiresAt: req.ExpiresAt, CreatedAt: now})
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
	return s.store.Update(ctx, func(w model.WriteStore) error {
		a, e := s.auth(w, raw)
		if e != nil {
			return e
		}
		t, e := w.Session(target)
		if !a.principal.Admin && errors.Is(e, model.ErrNotFound) {
			return model.ErrForbidden
		}
		if e != nil {
			return e
		}
		if !a.principal.Admin && t.ParentID != a.session.ID && t.ID != a.session.ID {
			return model.ErrForbidden
		}
		return revokedAgain(w.RevokeSessionTree(target, s.now().UTC()), t.RevokedAt)
	})
}

// Revocation is idempotent once authorized. Older stores report an already
// revoked root as missing, so trust the record read in this transaction.
func revokedAgain(err error, revokedAt *time.Time) error {
	if revokedAt != nil && errors.Is(err, model.ErrNotFound) {
		return nil
	}
	return err
}
