package comitia

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Service struct {
	store                    Store
	now                      Clock
	bootstrapCredential      string
	messageSchemas           map[string]PayloadValidator
	messageSchemaDescriptors map[string]MessageSchemaDescriptor
	maxContextDepth          int
	maxChildContextsPerGrant int
	messageNotifications     messageNotifications
}

func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalid)
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	if cfg.MaxContextDepth < 0 || cfg.MaxChildContextsPerGrant < 0 {
		return nil, fmt.Errorf("%w: context limits must be positive", ErrInvalid)
	}
	if cfg.MaxContextDepth == 0 {
		cfg.MaxContextDepth = 3
	}
	if cfg.MaxChildContextsPerGrant == 0 {
		cfg.MaxChildContextsPerGrant = 20
	}
	schemas := builtinMessageSchemas()
	for name, validator := range cfg.MessageSchemas {
		if name == "" || strings.HasPrefix(name, "comitia.") || validator == nil {
			return nil, fmt.Errorf("%w: invalid extension message schema", ErrInvalid)
		}
		schemas[name] = validator
	}
	svc := &Service{store: &observedStore{Store: cfg.Store}, now: cfg.Clock, bootstrapCredential: cfg.BootstrapCredential, messageSchemas: schemas, maxContextDepth: cfg.MaxContextDepth, maxChildContextsPerGrant: cfg.MaxChildContextsPerGrant}
	if err := svc.configureMessageSchemaDescriptors(cfg.MessageSchemaDescriptors); err != nil {
		return nil, err
	}
	return svc, nil
}

func id() string {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
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
	if d != OpenLease && (d < minLease || d > maxLease) {
		return Invalidf("lease must be between 1s and 24h, or open-ended")
	}
	return nil
}

func validSessionTTL(d time.Duration) error {
	if d < minSessionTTL {
		return Invalidf("ttl must be at least 1s")
	}
	return nil
}

type authInfo struct {
	session   Session
	principal Principal
	now       time.Time
}

func (s *Service) auth(r ReadStore, raw string) (authInfo, error) {
	var a authInfo
	if raw == "" {
		return a, ErrUnauthenticated
	}
	ss, e := r.SessionByTokenHash(tokenHash(raw))
	if e != nil {
		return a, ErrUnauthenticated
	}
	now := s.now().UTC()
	if ss.RevokedAt != nil || !ss.ExpiresAt.After(now) {
		return a, ErrUnauthenticated
	}
	p, e := r.Principal(ss.PrincipalID)
	if e != nil {
		return a, ErrUnauthenticated
	}
	return authInfo{session: ss, principal: p, now: now}, nil
}
func (s *Service) require(r ReadStore, raw, contextID string, permission Permission) (authInfo, []Grant, error) {
	a, e := s.auth(r, raw)
	if e != nil {
		return a, nil, e
	}
	return s.requireAuthenticated(r, a, contextID, permission)
}
func (s *Service) requireAuthenticated(r ReadStore, a authInfo, contextID string, permission Permission) (authInfo, []Grant, error) {
	gs, e := r.Grants(a.session.ID, contextID, a.now)
	if e != nil {
		return a, nil, e
	}
	for _, g := range gs {
		if g.Permissions&permission == permission {
			if permission&(PermissionWrite|PermissionClaim|PermissionCreate|PermissionAdmin) != 0 {
				if e := s.contextActiveAt(r, contextID, a.now); e != nil {
					return a, nil, e
				}
			}
			return a, gs, nil
		}
	}
	if permission != PermissionRead {
		return a, nil, ErrForbidden
	}
	// Read alone flows down from a parent into children created parent_readable; it returns no grants,
	// so it can never serve as a source for delegation.
	seen := map[string]bool{}
	for !seen[contextID] {
		seen[contextID] = true
		c, e := r.Context(contextID)
		if errors.Is(e, ErrNotFound) {
			return a, nil, ErrForbidden
		}
		if e != nil {
			return a, nil, e
		}
		if c.ParentContextID == "" || !c.ParentReadable {
			return a, nil, ErrForbidden
		}
		contextID = c.ParentContextID
		gs, e := r.Grants(a.session.ID, contextID, a.now)
		if e != nil {
			return a, nil, e
		}
		for _, g := range gs {
			if g.Permissions&PermissionRead != 0 {
				return a, nil, nil
			}
		}
	}
	return a, nil, ErrForbidden
}

func (s *Service) admin(r ReadStore, raw string) (authInfo, error) {
	a, e := s.auth(r, raw)
	if e != nil {
		return a, e
	}
	if !a.principal.Admin {
		return a, ErrForbidden
	}
	return a, nil
}

// Kept local to avoid exposing persistence implementation details in callers.
