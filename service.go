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
	store               Store
	now                 Clock
	bootstrapCredential string
	messageSchemas      map[string]PayloadValidator
}

func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalid)
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	schemas := builtinMessageSchemas()
	for name, validator := range cfg.MessageSchemas {
		if name == "" || strings.HasPrefix(name, "comitia.") || validator == nil {
			return nil, fmt.Errorf("%w: invalid extension message schema", ErrInvalid)
		}
		schemas[name] = validator
	}
	return &Service{store: cfg.Store, now: cfg.Clock, bootstrapCredential: cfg.BootstrapCredential, messageSchemas: schemas}, nil
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
func tokenHash(t string) string     { h := sha256.Sum256([]byte(t)); return hex.EncodeToString(h[:]) }
func validTTL(d time.Duration) bool { return d > 0 }

type authInfo struct {
	session   Session
	principal Principal
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
	return authInfo{ss, p}, nil
}
func (s *Service) require(r ReadStore, raw, contextID string, permission Permission) (authInfo, []Grant, error) {
	a, e := s.auth(r, raw)
	if e != nil {
		return a, nil, e
	}
	gs, e := r.Grants(a.session.ID, contextID, s.now().UTC())
	if e != nil {
		return a, nil, e
	}
	for _, g := range gs {
		if g.Permissions&permission == permission {
			return a, gs, nil
		}
	}
	if permission != PermissionRead {
		return a, nil, ErrForbidden
	}
	// Read alone flows down from a parent into children created parent_readable; it returns no grants,
	// so it can never serve as a source for delegation.
	for depth := 0; depth < maxContextDepth; depth++ {
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
		gs, e := r.Grants(a.session.ID, contextID, s.now().UTC())
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

// maxContextDepth bounds the parent walk for inherited read.
const maxContextDepth = 64

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
