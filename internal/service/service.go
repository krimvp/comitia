package service

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/krimvp/comitia/internal/model"

	"github.com/krimvp/comitia/internal/schema"
)

type Service struct {
	store                    model.Store
	now                      model.Clock
	bootstrapCredential      string
	messageSchemas           map[string]model.PayloadValidator
	messageSchemaDescriptors map[string]model.MessageSchemaDescriptor
	maxContextDepth          int
	maxChildContextsPerGrant int
	messageNotifications     messageNotifications
	waitTicker               func() (<-chan time.Time, func()) // tests replace the wait poll
}

func New(cfg model.Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: store is required", model.ErrInvalid)
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.MaxContextDepth < 0 || cfg.MaxChildContextsPerGrant < 0 {
		return nil, fmt.Errorf("%w: context limits must be positive", model.ErrInvalid)
	}
	if cfg.MaxContextDepth == 0 {
		cfg.MaxContextDepth = 3
	}
	if cfg.MaxChildContextsPerGrant == 0 {
		cfg.MaxChildContextsPerGrant = 20
	}
	schemas := schema.BuiltinMessageSchemas()
	for name, validator := range cfg.MessageSchemas {
		if name == "" || strings.HasPrefix(name, "comitia.") || validator == nil {
			return nil, fmt.Errorf("%w: invalid extension message schema", model.ErrInvalid)
		}
		schemas[name] = validator
	}
	svc := &Service{store: &observedStore{Store: cfg.Store}, now: cfg.Clock, bootstrapCredential: cfg.BootstrapCredential, messageSchemas: schemas, maxContextDepth: cfg.MaxContextDepth, maxChildContextsPerGrant: cfg.MaxChildContextsPerGrant}
	if err := svc.configureMessageSchemaDescriptors(cfg.MessageSchemaDescriptors); err != nil {
		return nil, err
	}
	return svc, nil
}

func token() string {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return "ct_" + hex.EncodeToString(b[:])
}

func tokenHash(t string) string { h := sha256.Sum256([]byte(t)); return hex.EncodeToString(h[:]) }

// Lease and session TTL bounds, enforced here so every adapter shares them.
const (
	minLease      = time.Second
	maxLease      = 24 * time.Hour
	minSessionTTL = time.Second
)

// validLease admits OpenLease or a lease between minLease and maxLease.
func validLease(d time.Duration) error {
	if d != model.OpenLease && (d < minLease || d > maxLease) {
		return model.Invalidf("lease must be between 1s and 24h, or open-ended")
	}
	return nil
}

func validSessionTTL(d time.Duration) error {
	if d < minSessionTTL {
		return model.Invalidf("ttl must be at least 1s")
	}
	return nil
}

type authInfo struct {
	session   model.Session
	principal model.Principal
	now       time.Time
}

func (s *Service) auth(r model.ReadStore, raw string) (authInfo, error) {
	var a authInfo
	if raw == "" {
		return a, model.ErrUnauthenticated
	}
	ss, e := r.SessionByTokenHash(tokenHash(raw))
	if e != nil {
		return a, model.ErrUnauthenticated
	}
	now := s.now().UTC()
	if ss.RevokedAt != nil || !ss.ExpiresAt.After(now) {
		return a, model.ErrUnauthenticated
	}
	p, e := r.Principal(ss.PrincipalID)
	if e != nil {
		return a, model.ErrUnauthenticated
	}
	return authInfo{session: ss, principal: p, now: now}, nil
}

func (s *Service) require(r model.ReadStore, raw, contextID string, permission model.Permission) (authInfo, []model.Grant, error) {
	a, e := s.auth(r, raw)
	if e != nil {
		return a, nil, e
	}
	return s.requireAuthenticated(r, a, contextID, permission)
}

func (s *Service) requireAuthenticated(r model.ReadStore, a authInfo, contextID string, permission model.Permission) (authInfo, []model.Grant, error) {
	gs, e := r.Grants(a.session.ID, contextID, a.now)
	if e != nil {
		return a, nil, e
	}
	for _, g := range gs {
		if g.Permissions&permission == permission {
			if permission&(model.PermissionWrite|model.PermissionClaim|model.PermissionCreate|model.PermissionAdmin) != 0 {
				if e := s.contextActiveAt(r, contextID, a.now); e != nil {
					return a, nil, e
				}
			}
			return a, gs, nil
		}
	}
	if permission != model.PermissionRead {
		return a, nil, model.ErrForbidden
	}
	// Read alone flows down from a parent into children created parent_readable; it returns no grants,
	// so it can never serve as a source for delegation.
	seen := map[string]bool{}
	for !seen[contextID] {
		seen[contextID] = true
		c, e := r.Context(contextID)
		if errors.Is(e, model.ErrNotFound) {
			return a, nil, model.ErrForbidden
		}
		if e != nil {
			return a, nil, e
		}
		if c.ParentContextID == "" || !c.ParentReadable {
			return a, nil, model.ErrForbidden
		}
		contextID = c.ParentContextID
		gs, e := r.Grants(a.session.ID, contextID, a.now)
		if e != nil {
			return a, nil, e
		}
		for _, g := range gs {
			if g.Permissions&model.PermissionRead != 0 {
				return a, nil, nil
			}
		}
	}
	return a, nil, model.ErrForbidden
}

func (s *Service) admin(r model.ReadStore, raw string) (authInfo, error) {
	a, e := s.auth(r, raw)
	if e != nil {
		return a, e
	}
	if !a.principal.Admin {
		return a, model.ErrForbidden
	}
	return a, nil
}

// Kept local to avoid exposing persistence implementation details in callers.
