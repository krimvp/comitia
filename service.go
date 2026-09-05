package comitia

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
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
}

func New(cfg Config) (*Service, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("%w: store is required", ErrInvalid)
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &Service{store: cfg.Store, now: cfg.Clock, bootstrapCredential: cfg.BootstrapCredential}, nil
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

func (s *Service) Bootstrap(ctx context.Context, in BootstrapInput) (Principal, Credential, error) {
	var p Principal
	var c Credential
	if s.bootstrapCredential == "" || subtle.ConstantTimeCompare([]byte(in.Credential), []byte(s.bootstrapCredential)) != 1 {
		return p, c, ErrUnauthenticated
	}
	if strings.TrimSpace(in.AdminName) == "" || !validTTL(in.SessionTTL) {
		return p, c, ErrInvalid
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
		p = Principal{ID: id(), Name: in.AdminName, Admin: true, CreatedAt: now}
		raw := token()
		c = Credential{Token: raw, Session: Session{ID: id(), PrincipalID: p.ID, ExpiresAt: now.Add(in.SessionTTL), CreatedAt: now}}
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

func (s *Service) CreatePrincipal(ctx context.Context, raw, name string) (Principal, error) {
	var p Principal
	if strings.TrimSpace(name) == "" {
		return p, ErrInvalid
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		if _, e := s.admin(w, raw); e != nil {
			return e
		}
		p = Principal{ID: id(), Name: name, CreatedAt: s.now().UTC()}
		return w.PutPrincipal(p)
	})
	return p, e
}
func (s *Service) MintSession(ctx context.Context, raw, principalID string, ttl time.Duration) (Credential, error) {
	var c Credential
	if !validTTL(ttl) {
		return c, ErrInvalid
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
		c = Credential{Token: t, Session: Session{ID: id(), PrincipalID: principalID, ExpiresAt: now.Add(ttl), CreatedAt: now}}
		return w.PutSession(c.Session, tokenHash(t))
	})
	return c, e
}
func (s *Service) DelegateSession(ctx context.Context, raw string, in DelegateInput) (Principal, Credential, []Grant, error) {
	var p Principal
	var c Credential
	var out []Grant
	if strings.TrimSpace(in.PrincipalName) == "" || !validTTL(in.TTL) || len(in.Grants) == 0 {
		return p, c, nil, ErrInvalid
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
		p = Principal{ID: id(), Name: in.PrincipalName, CreatedAt: now}
		tok := token()
		c = Credential{Token: tok, Session: Session{ID: id(), PrincipalID: p.ID, ParentID: parent.session.ID, ExpiresAt: expiry, CreatedAt: now}}
		for _, req := range in.Grants {
			if req.Permissions == 0 || req.ExpiresAt.After(expiry) || !req.ExpiresAt.After(now) {
				return ErrInvalid
			}
			_, parents, e := s.require(w, raw, req.ContextID, PermissionGrant)
			if e != nil {
				return e
			}
			var source *Grant
			for i := range parents {
				g := &parents[i]
				if g.Permissions&(req.Permissions|PermissionGrant) == (req.Permissions|PermissionGrant) && !req.ExpiresAt.After(g.ExpiresAt) {
					source = g
					break
				}
			}
			if source == nil {
				return ErrForbidden
			}
			out = append(out, Grant{ID: id(), SessionID: c.Session.ID, ContextID: req.ContextID, ParentGrantID: source.ID, IssuerSessionID: parent.session.ID, Permissions: req.Permissions, ExpiresAt: req.ExpiresAt, CreatedAt: now})
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
		if e != nil {
			return e
		}
		if !a.principal.Admin && t.ParentID != a.session.ID {
			return ErrForbidden
		}
		return w.RevokeSessionTree(target, s.now().UTC())
	})
}
func (s *Service) RevokeGrant(ctx context.Context, raw, grantID string) error {
	return s.store.Update(ctx, func(w WriteStore) error {
		a, e := s.auth(w, raw)
		if e != nil {
			return e
		}
		if !a.principal.Admin {
			return ErrForbidden
		}
		return w.RevokeGrantTree(grantID, s.now().UTC())
	})
}

func (s *Service) CreateWorkspace(ctx context.Context, raw, name string) (Workspace, error) {
	var x Workspace
	if strings.TrimSpace(name) == "" {
		return x, ErrInvalid
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		if _, e := s.admin(w, raw); e != nil {
			return e
		}
		x = Workspace{ID: id(), Name: name, CreatedAt: s.now().UTC()}
		return w.PutWorkspace(x)
	})
	return x, e
}
func (s *Service) CreateContext(ctx context.Context, raw string, in CreateContextInput) (WorkContext, error) {
	var x WorkContext
	if strings.TrimSpace(in.Name) == "" {
		return x, ErrInvalid
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		a, e := s.admin(w, raw)
		if e != nil {
			return e
		}
		if _, e = w.Workspace(in.WorkspaceID); e != nil {
			return e
		}
		x = WorkContext{ID: id(), WorkspaceID: in.WorkspaceID, Name: in.Name, CreatedAt: s.now().UTC()}
		if e = w.PutContext(x); e != nil {
			return e
		}
		return w.PutGrant(Grant{ID: id(), SessionID: a.session.ID, ContextID: x.ID, IssuerSessionID: a.session.ID, Permissions: PermissionAll, ExpiresAt: a.session.ExpiresAt, CreatedAt: s.now().UTC()})
	})
	return x, e
}
func (s *Service) GetContext(ctx context.Context, raw, cid string) (WorkContext, error) {
	var x WorkContext
	e := s.store.View(ctx, func(r ReadStore) error {
		if _, _, e := s.require(r, raw, cid, PermissionRead); e != nil {
			return e
		}
		var e error
		x, e = r.Context(cid)
		return e
	})
	return x, e
}
func (s *Service) AddReference(ctx context.Context, raw string, in AddReferenceInput) (Reference, error) {
	var x Reference
	if in.Kind == "" || in.URI == "" {
		return x, ErrInvalid
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		if _, _, e := s.require(w, raw, in.ContextID, PermissionWrite); e != nil {
			return e
		}
		x = Reference{ID: id(), ContextID: in.ContextID, Kind: in.Kind, URI: in.URI, CreatedAt: s.now().UTC()}
		return w.PutReference(x)
	})
	return x, e
}
func (s *Service) GrantContext(ctx context.Context, raw string, in GrantInput) (Grant, error) {
	var g Grant
	e := s.store.Update(ctx, func(w WriteStore) error {
		a, e := s.auth(w, raw)
		if e != nil {
			return e
		}
		if _, e = w.Context(in.ContextID); e != nil {
			return e
		}
		target, e := w.Session(in.SessionID)
		if e != nil {
			return e
		}
		if target.ParentID != a.session.ID && !a.principal.Admin {
			return ErrForbidden
		}
		if in.Permissions == 0 || !in.ExpiresAt.After(s.now().UTC()) || in.ExpiresAt.After(target.ExpiresAt) || in.ExpiresAt.After(a.session.ExpiresAt) {
			return ErrInvalid
		}
		if a.principal.Admin {
			g = Grant{ID: id(), SessionID: in.SessionID, ContextID: in.ContextID, IssuerSessionID: a.session.ID, Permissions: in.Permissions, ExpiresAt: in.ExpiresAt, CreatedAt: s.now().UTC()}
			return w.PutGrant(g)
		}
		parents, e := w.Grants(a.session.ID, in.ContextID, s.now().UTC())
		if e != nil {
			return e
		}
		var source *Grant
		for i := range parents {
			p := &parents[i]
			if p.Permissions&(in.Permissions|PermissionGrant) == (in.Permissions|PermissionGrant) && !in.ExpiresAt.After(p.ExpiresAt) {
				source = p
				break
			}
		}
		if source == nil {
			return ErrForbidden
		}
		g = Grant{ID: id(), SessionID: in.SessionID, ContextID: in.ContextID, ParentGrantID: source.ID, IssuerSessionID: a.session.ID, Permissions: in.Permissions, ExpiresAt: in.ExpiresAt, CreatedAt: s.now().UTC()}
		return w.PutGrant(g)
	})
	return g, e
}

func (s *Service) AppendMessage(ctx context.Context, raw, cid string, in MessageInput) (Message, error) {
	var m Message
	if in.DedupeKey == "" || in.Body == "" {
		return m, ErrInvalid
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		a, _, e := s.require(w, raw, cid, PermissionWrite)
		if e != nil {
			return e
		}
		old, e := w.MessageByDedupe(cid, a.session.ID, in.DedupeKey)
		if e == nil {
			if old.Body != in.Body {
				return ErrConflict
			}
			m = old
			return nil
		}
		if !errors.Is(e, ErrNotFound) {
			return e
		}
		m = Message{ID: id(), ContextID: cid, SessionID: a.session.ID, DedupeKey: in.DedupeKey, Body: in.Body, CreatedAt: s.now().UTC()}
		m, e = w.PutMessage(m)
		return e
	})
	return m, e
}
func (s *Service) ReplayMessages(ctx context.Context, raw, cid string, after int64, limit int) ([]Message, error) {
	var out []Message
	if after < 0 || limit < 1 || limit > 1000 {
		return nil, ErrInvalid
	}
	e := s.store.View(ctx, func(r ReadStore) error {
		if _, _, e := s.require(r, raw, cid, PermissionRead); e != nil {
			return e
		}
		var e error
		out, e = r.Messages(cid, after, limit)
		return e
	})
	return out, e
}
func (s *Service) AckMessages(ctx context.Context, raw, cid string, seq int64) error {
	if seq < 0 {
		return ErrInvalid
	}
	return s.store.Update(ctx, func(w WriteStore) error {
		a, _, e := s.require(w, raw, cid, PermissionRead)
		if e != nil {
			return e
		}
		return w.Ack(a.session.ID, cid, seq, s.now().UTC())
	})
}

func (s *Service) MessageCursor(ctx context.Context, raw, cid string) (int64, error) {
	var cursor int64
	e := s.store.View(ctx, func(r ReadStore) error {
		a, _, e := s.require(r, raw, cid, PermissionRead)
		if e != nil {
			return e
		}
		cursor, e = r.AckCursor(a.session.ID, cid)
		return e
	})
	return cursor, e
}

func (s *Service) CreateTask(ctx context.Context, raw, cid string, in TaskInput) (Task, error) {
	var t Task
	if in.Title == "" {
		return t, ErrInvalid
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		if _, _, e := s.require(w, raw, cid, PermissionWrite); e != nil {
			return e
		}
		now := s.now().UTC()
		t = Task{ID: id(), ContextID: cid, Title: in.Title, Body: in.Body, Status: TaskPending, CreatedAt: now, UpdatedAt: now}
		return w.PutTask(t)
	})
	return t, e
}
func (s *Service) GetTask(ctx context.Context, raw, id string) (Task, error) {
	var t Task
	e := s.store.View(ctx, func(r ReadStore) error {
		found, e := r.Task(id)
		if e != nil {
			return e
		}
		if _, _, e = s.require(r, raw, found.ContextID, PermissionRead); e != nil {
			return e
		}
		found.LeaseTokenHash = ""
		t = found
		return nil
	})
	if e != nil {
		return Task{}, e
	}
	return t, nil
}
func (s *Service) ListTasks(ctx context.Context, raw, cid string, status TaskStatus, limit int) ([]Task, error) {
	var out []Task
	if limit < 1 || limit > 1000 {
		return nil, ErrInvalid
	}
	e := s.store.View(ctx, func(r ReadStore) error {
		if _, _, e := s.require(r, raw, cid, PermissionRead); e != nil {
			return e
		}
		var e error
		out, e = r.Tasks(cid, status, limit)
		return e
	})
	return out, e
}
func (s *Service) ClaimTask(ctx context.Context, raw, taskID string, lease time.Duration) (TaskClaim, error) {
	var c TaskClaim
	if !validTTL(lease) {
		return c, ErrInvalid
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		t, e := w.Task(taskID)
		if e != nil {
			return e
		}
		a, _, e := s.require(w, raw, t.ContextID, PermissionClaim)
		if e != nil {
			return e
		}
		now := s.now().UTC()
		if t.Status == TaskCompleted {
			return ErrConflict
		}
		if t.LeaseExpiresAt != nil && t.LeaseExpiresAt.After(now) {
			return ErrConflict
		}
		t.Status = TaskLeased
		t.LeaseOwnerSessionID = a.session.ID
		t.LeaseVersion++
		exp := now.Add(lease)
		if exp.After(a.session.ExpiresAt) {
			exp = a.session.ExpiresAt
		}
		t.LeaseExpiresAt = &exp
		t.UpdatedAt = now
		c = TaskClaim{Task: t, Token: token()}
		t.LeaseTokenHash = tokenHash(c.Token)
		c.Task = t
		c.Task.LeaseTokenHash = ""
		return w.PutTask(t)
	})
	return c, e
}
func (s *Service) RenewClaim(ctx context.Context, raw, taskID, claimToken string, version int64, lease time.Duration) (TaskClaim, error) {
	return s.changeLease(ctx, raw, taskID, claimToken, version, lease, false)
}
func (s *Service) CompleteTask(ctx context.Context, raw, taskID, claimToken string, version int64) (Task, error) {
	c, e := s.changeLease(ctx, raw, taskID, claimToken, version, 0, true)
	return c.Task, e
}
func (s *Service) changeLease(ctx context.Context, raw, taskID, claimToken string, version int64, lease time.Duration, complete bool) (TaskClaim, error) {
	var c TaskClaim
	if !complete && !validTTL(lease) {
		return c, ErrInvalid
	}
	e := s.store.Update(ctx, func(w WriteStore) error {
		t, e := w.Task(taskID)
		if e != nil {
			return e
		}
		a, _, e := s.require(w, raw, t.ContextID, PermissionClaim)
		if e != nil {
			return e
		}
		now := s.now().UTC()
		if t.Status != TaskLeased || t.LeaseOwnerSessionID != a.session.ID || t.LeaseVersion != version || t.LeaseExpiresAt == nil || !t.LeaseExpiresAt.After(now) || subtle.ConstantTimeCompare([]byte(t.LeaseTokenHash), []byte(tokenHash(claimToken))) != 1 {
			return ErrStaleClaim
		}
		if complete {
			t.Status = TaskCompleted
			t.LeaseExpiresAt = nil
			t.LeaseTokenHash = ""
		} else {
			exp := now.Add(lease)
			if exp.After(a.session.ExpiresAt) {
				exp = a.session.ExpiresAt
			}
			t.LeaseExpiresAt = &exp
			t.LeaseVersion++
			c.Token = token()
			t.LeaseTokenHash = tokenHash(c.Token)
		}
		t.UpdatedAt = now
		c.Task = t
		c.Task.LeaseTokenHash = ""
		return w.PutTask(t)
	})
	return c, e
}

// Kept local to avoid exposing persistence implementation details in callers.
