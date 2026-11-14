package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
)

func inviteSession(t *testing.T, f fixture, name string, ttl time.Duration) model.Credential {
	t.Helper()
	p, err := f.svc.CreatePrincipal(context.Background(), f.admin.Token, name)
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.svc.MintSession(context.Background(), f.admin.Token, p.ID, ttl)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func inviteGrant(t *testing.T, f fixture, c model.Credential, cid string, p model.Permission, ttl time.Duration) model.Grant {
	t.Helper()
	g, err := f.svc.GrantContext(context.Background(), f.admin.Token, model.GrantInput{SessionID: c.Session.ID, ContextID: cid, Permissions: p, ExpiresAt: f.now.Add(ttl)})
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func inviteContext(t *testing.T, f fixture, wid string) model.WorkContext {
	t.Helper()
	c, err := f.svc.CreateContext(context.Background(), f.admin.Token, model.CreateContextInput{WorkspaceID: wid, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGrantInviteAndRevocationCascade(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	issuer := inviteSession(t, f, "issuer", 8*time.Hour)
	source := inviteGrant(t, f, issuer, f.context.ID, model.PermissionRead|model.PermissionGrant, 4*time.Hour)
	target := inviteSession(t, f, "target", 8*time.Hour)
	other := inviteContext(t, f, f.context.WorkspaceID)
	inviteGrant(t, f, target, other.ID, model.PermissionWrite, 4*time.Hour)
	if _, err := f.svc.GetContext(ctx, target.Token, f.context.ID); !errors.Is(err, model.ErrForbidden) {
		t.Fatalf("before invite: %v", err)
	}
	g, err := f.svc.GrantContext(ctx, issuer.Token, model.GrantInput{SessionID: target.Session.ID, ContextID: f.context.ID, Permissions: model.PermissionRead | model.PermissionGrant, ExpiresAt: f.now.Add(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if g.ParentGrantID != source.ID || g.IssuerSessionID != issuer.Session.ID || g.Permissions != model.PermissionRead|model.PermissionGrant {
		t.Fatalf("grant: %+v", g)
	}
	if _, err = f.svc.GetContext(ctx, target.Token, f.context.ID); err != nil {
		t.Fatal(err)
	}
	_, child, _, err := f.svc.DelegateSession(ctx, target.Token, model.DelegateInput{PrincipalName: "invitee-child", TTL: time.Hour, Grants: []model.DelegatedGrant{{ContextID: f.context.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.RevokeGrant(ctx, f.admin.Token, source.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []model.Credential{target, child} {
		if _, err = f.svc.GetContext(ctx, c.Token, f.context.ID); !errors.Is(err, model.ErrForbidden) {
			t.Fatalf("cascade %s: %v", c.Session.ID, err)
		}
	}
}

func TestGrantInviteRestrictions(t *testing.T) {
	cases := []struct {
		name string
		want error
	}{
		{"read subset", nil}, {"missing permission", model.ErrForbidden}, {"missing grant", model.ErrForbidden},
		{"split permissions", model.ErrForbidden}, {"issuer grant expiry", model.ErrForbidden}, {"target session expiry", model.ErrInvalid},
		{"issuer session expiry", model.ErrInvalid}, {"past expiry", model.ErrInvalid}, {"zero permissions", model.ErrInvalid},
		{"other workspace", model.ErrForbidden}, {"no membership", model.ErrForbidden}, {"same principal different session", model.ErrForbidden},
		{"expired membership", model.ErrForbidden}, {"revoked membership", model.ErrForbidden}, {"revoked membership parent", model.ErrForbidden},
		{"revoked membership issuer", model.ErrForbidden}, {"expired membership issuer", model.ErrForbidden},
		{"revoked target", model.ErrForbidden}, {"expired target", model.ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			ctx := context.Background()
			issuer := inviteSession(t, f, "issuer", 8*time.Hour)
			perms := model.PermissionRead | model.PermissionGrant
			if tc.name == "missing grant" {
				perms = model.PermissionRead
			}
			if tc.name == "split permissions" {
				perms = model.PermissionGrant
			}
			inviteGrant(t, f, issuer, f.context.ID, perms, 4*time.Hour)
			if tc.name == "split permissions" {
				inviteGrant(t, f, issuer, f.context.ID, model.PermissionRead, 4*time.Hour)
			}
			targetTTL := 8 * time.Hour
			if tc.name == "expired target" {
				targetTTL = time.Hour
			}
			target := inviteSession(t, f, "target", targetTTL)
			other := inviteContext(t, f, f.context.WorkspaceID)
			membershipTTL := min(4*time.Hour, targetTTL)
			if tc.name == "other workspace" {
				ws, err := f.svc.CreateWorkspace(ctx, f.admin.Token, "elsewhere")
				if err != nil {
					t.Fatal(err)
				}
				other = inviteContext(t, f, ws.ID)
			}
			if tc.name == "expired membership" {
				membershipTTL = time.Minute
			}
			membership := inviteGrant(t, f, target, other.ID, model.PermissionRead, membershipTTL)
			in := model.GrantInput{SessionID: target.Session.ID, ContextID: f.context.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(2 * time.Hour)}
			switch tc.name {
			case "missing permission":
				in.Permissions = model.PermissionWrite
			case "issuer grant expiry":
				in.ExpiresAt = f.now.Add(5 * time.Hour)
			case "target session expiry":
				target = inviteSession(t, f, "short", time.Hour)
				inviteGrant(t, f, target, other.ID, model.PermissionRead, time.Hour)
				in.SessionID = target.Session.ID
			case "issuer session expiry":
				in.ExpiresAt = f.now.Add(9 * time.Hour)
			case "past expiry":
				in.ExpiresAt = *f.now
			case "zero permissions":
				in.Permissions = 0
			case "no membership":
				target = inviteSession(t, f, "outsider", 8*time.Hour)
				in.SessionID = target.Session.ID
			case "same principal different session":
				c, err := f.svc.MintSession(ctx, f.admin.Token, target.Session.PrincipalID, 8*time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				in.SessionID = c.Session.ID
			case "expired membership":
				*f.now = f.now.Add(2 * time.Minute)
			case "revoked membership":
				if err := f.svc.RevokeGrant(ctx, f.admin.Token, membership.ID); err != nil {
					t.Fatal(err)
				}
			case "revoked membership parent", "revoked membership issuer", "expired membership issuer":
				memberIssuer := inviteSession(t, f, "member issuer", 3*time.Hour)
				pg := inviteGrant(t, f, memberIssuer, other.ID, model.PermissionRead|model.PermissionGrant, 3*time.Hour)
				_, err := f.svc.GrantContext(ctx, memberIssuer.Token, model.GrantInput{SessionID: target.Session.ID, ContextID: other.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(3 * time.Hour)})
				if err != nil {
					t.Fatal(err)
				}
				if err = f.svc.RevokeGrant(ctx, f.admin.Token, membership.ID); err != nil {
					t.Fatal(err)
				}
				if tc.name == "revoked membership parent" {
					if err = f.svc.RevokeGrant(ctx, f.admin.Token, pg.ID); err != nil {
						t.Fatal(err)
					}
				} else if tc.name == "revoked membership issuer" {
					if err = f.svc.RevokeSession(ctx, f.admin.Token, memberIssuer.Session.ID); err != nil {
						t.Fatal(err)
					}
				} else {
					*f.now = f.now.Add(3 * time.Hour)
					in.ExpiresAt = f.now.Add(30 * time.Minute)
				}
			case "revoked target":
				if err := f.svc.RevokeSession(ctx, f.admin.Token, target.Session.ID); err != nil {
					t.Fatal(err)
				}
			case "expired target":
				*f.now = f.now.Add(time.Hour)
			}
			g, err := f.svc.GrantContext(ctx, issuer.Token, in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %+v, %v; want %v", g, err, tc.want)
			}
			if tc.want != nil && g.ID != "" {
				t.Fatalf("rejected grant populated: %+v", g)
			}
		})
	}
}

func TestGrantInviteOwnMembershipAndChildPath(t *testing.T) {
	for _, mode := range []string{"ancestor only", "direct child", "admin"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			ctx := context.Background()
			issuer := inviteSession(t, f, "issuer", 8*time.Hour)
			source := inviteGrant(t, f, issuer, f.context.ID, model.PermissionRead|model.PermissionGrant, 4*time.Hour)
			target := inviteSession(t, f, "target", 8*time.Hour)
			parent := inviteSession(t, f, "ancestor", 8*time.Hour)
			other := inviteContext(t, f, f.context.WorkspaceID)
			inviteGrant(t, f, parent, other.ID, model.PermissionRead, 4*time.Hour)
			target.Session.ParentID = parent.Session.ID
			if mode == "direct child" {
				target.Session.ParentID = issuer.Session.ID
			}
			if mode == "admin" {
				target.Session.ParentID = ""
			}
			// An unrelated child without its own grants must not inherit membership.
			target.Session.ID = model.NewID()
			err := f.store.Update(ctx, func(w model.WriteStore) error { return w.PutSession(target.Session, tokenHash(token())) })
			if err != nil {
				t.Fatal(err)
			}
			raw := issuer.Token
			if mode == "admin" {
				raw = f.admin.Token
			}
			g, err := f.svc.GrantContext(ctx, raw, model.GrantInput{SessionID: target.Session.ID, ContextID: f.context.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(time.Hour)})
			if mode == "ancestor only" {
				if !errors.Is(err, model.ErrForbidden) {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "admin" {
				if g.ParentGrantID != "" {
					t.Fatalf("admin grant %+v", g)
				}
			} else if g.ParentGrantID != source.ID {
				t.Fatalf("parent %+v", g)
			}
		})
	}
}

func TestGrantInviteMembershipAfterFirstCandidatePage(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	issuer := inviteSession(t, f, "issuer", 8*time.Hour)
	inviteGrant(t, f, issuer, f.context.ID, model.PermissionRead|model.PermissionGrant, 4*time.Hour)
	target := inviteSession(t, f, "target", 8*time.Hour)
	ws, err := f.svc.CreateWorkspace(ctx, f.admin.Token, "elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	err = f.store.Update(ctx, func(w model.WriteStore) error {
		for i := 0; i <= 100; i++ {
			wid := ws.ID
			if i == 100 {
				wid = f.context.WorkspaceID
			}
			c := model.WorkContext{ID: fmt.Sprintf("candidate-%03d", i), WorkspaceID: wid, Name: "candidate", CreatedAt: *f.now}
			if e := w.PutContext(c); e != nil {
				return e
			}
			if e := w.PutGrant(model.Grant{ID: model.NewID(), SessionID: target.Session.ID, ContextID: c.ID, IssuerSessionID: f.admin.Session.ID, Permissions: model.PermissionWrite, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.GrantContext(ctx, issuer.Token, model.GrantInput{SessionID: target.Session.ID, ContextID: f.context.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
}
