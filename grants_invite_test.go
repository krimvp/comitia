package comitia

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func inviteSession(t *testing.T, f fixture, name string, ttl time.Duration) Credential {
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
func inviteGrant(t *testing.T, f fixture, c Credential, cid string, p Permission, ttl time.Duration) Grant {
	t.Helper()
	g, err := f.svc.GrantContext(context.Background(), f.admin.Token, GrantInput{SessionID: c.Session.ID, ContextID: cid, Permissions: p, ExpiresAt: f.now.Add(ttl)})
	if err != nil {
		t.Fatal(err)
	}
	return g
}
func inviteContext(t *testing.T, f fixture, wid string) WorkContext {
	t.Helper()
	c, err := f.svc.CreateContext(context.Background(), f.admin.Token, CreateContextInput{WorkspaceID: wid, Name: "other"})
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
	source := inviteGrant(t, f, issuer, f.context.ID, PermissionRead|PermissionGrant, 4*time.Hour)
	target := inviteSession(t, f, "target", 8*time.Hour)
	other := inviteContext(t, f, f.context.WorkspaceID)
	inviteGrant(t, f, target, other.ID, PermissionWrite, 4*time.Hour)
	if _, err := f.svc.GetContext(ctx, target.Token, f.context.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("before invite: %v", err)
	}
	g, err := f.svc.GrantContext(ctx, issuer.Token, GrantInput{SessionID: target.Session.ID, ContextID: f.context.ID, Permissions: PermissionRead | PermissionGrant, ExpiresAt: f.now.Add(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if g.ParentGrantID != source.ID || g.IssuerSessionID != issuer.Session.ID || g.Permissions != PermissionRead|PermissionGrant {
		t.Fatalf("grant: %+v", g)
	}
	if _, err = f.svc.GetContext(ctx, target.Token, f.context.ID); err != nil {
		t.Fatal(err)
	}
	_, child, _, err := f.svc.DelegateSession(ctx, target.Token, DelegateInput{PrincipalName: "invitee-child", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.svc.RevokeGrant(ctx, f.admin.Token, source.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range []Credential{target, child} {
		if _, err = f.svc.GetContext(ctx, c.Token, f.context.ID); !errors.Is(err, ErrForbidden) {
			t.Fatalf("cascade %s: %v", c.Session.ID, err)
		}
	}
}

func TestGrantInviteRestrictions(t *testing.T) {
	cases := []struct {
		name string
		want error
	}{
		{"read subset", nil}, {"missing permission", ErrForbidden}, {"missing grant", ErrForbidden},
		{"split permissions", ErrForbidden}, {"issuer grant expiry", ErrForbidden}, {"target session expiry", ErrInvalid},
		{"issuer session expiry", ErrInvalid}, {"past expiry", ErrInvalid}, {"zero permissions", ErrInvalid},
		{"other workspace", ErrForbidden}, {"no membership", ErrForbidden}, {"same principal different session", ErrForbidden},
		{"expired membership", ErrForbidden}, {"revoked membership", ErrForbidden}, {"revoked membership parent", ErrForbidden},
		{"revoked membership issuer", ErrForbidden}, {"expired membership issuer", ErrForbidden},
		{"revoked target", ErrForbidden}, {"expired target", ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			ctx := context.Background()
			issuer := inviteSession(t, f, "issuer", 8*time.Hour)
			perms := PermissionRead | PermissionGrant
			if tc.name == "missing grant" {
				perms = PermissionRead
			}
			if tc.name == "split permissions" {
				perms = PermissionGrant
			}
			inviteGrant(t, f, issuer, f.context.ID, perms, 4*time.Hour)
			if tc.name == "split permissions" {
				inviteGrant(t, f, issuer, f.context.ID, PermissionRead, 4*time.Hour)
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
			membership := inviteGrant(t, f, target, other.ID, PermissionRead, membershipTTL)
			in := GrantInput{SessionID: target.Session.ID, ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(2 * time.Hour)}
			switch tc.name {
			case "missing permission":
				in.Permissions = PermissionWrite
			case "issuer grant expiry":
				in.ExpiresAt = f.now.Add(5 * time.Hour)
			case "target session expiry":
				target = inviteSession(t, f, "short", time.Hour)
				inviteGrant(t, f, target, other.ID, PermissionRead, time.Hour)
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
				pg := inviteGrant(t, f, memberIssuer, other.ID, PermissionRead|PermissionGrant, 3*time.Hour)
				_, err := f.svc.GrantContext(ctx, memberIssuer.Token, GrantInput{SessionID: target.Session.ID, ContextID: other.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(3 * time.Hour)})
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

func TestGrantInviteSessionAncestryAndChildPath(t *testing.T) {
	for _, mode := range []string{"ancestor", "revoked ancestor", "expired ancestor", "direct child", "admin"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			ctx := context.Background()
			issuer := inviteSession(t, f, "issuer", 8*time.Hour)
			source := inviteGrant(t, f, issuer, f.context.ID, PermissionRead|PermissionGrant, 4*time.Hour)
			target := inviteSession(t, f, "target", 8*time.Hour)
			parentTTL := 8 * time.Hour
			if mode == "expired ancestor" {
				parentTTL = time.Hour
			}
			parent := inviteSession(t, f, "ancestor", parentTTL)
			other := inviteContext(t, f, f.context.WorkspaceID)
			inviteGrant(t, f, parent, other.ID, PermissionRead, min(4*time.Hour, parentTTL))
			target.Session.ParentID = parent.Session.ID
			if mode == "direct child" {
				target.Session.ParentID = issuer.Session.ID
			}
			if mode == "admin" {
				target.Session.ParentID = ""
			}
			// A child without its own grants exercises workspace eligibility via ancestry.
			target.Session.ID = id()
			err := f.store.Update(ctx, func(w WriteStore) error { return w.PutSession(target.Session, tokenHash(token())) })
			if err != nil {
				t.Fatal(err)
			}
			if mode == "revoked ancestor" {
				if err = f.svc.RevokeSession(ctx, f.admin.Token, parent.Session.ID); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "expired ancestor" {
				*f.now = f.now.Add(time.Hour)
			}
			raw := issuer.Token
			if mode == "admin" {
				raw = f.admin.Token
			}
			g, err := f.svc.GrantContext(ctx, raw, GrantInput{SessionID: target.Session.ID, ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour)})
			if mode == "revoked ancestor" || mode == "expired ancestor" {
				if !errors.Is(err, ErrForbidden) {
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
	inviteGrant(t, f, issuer, f.context.ID, PermissionRead|PermissionGrant, 4*time.Hour)
	target := inviteSession(t, f, "target", 8*time.Hour)
	ws, err := f.svc.CreateWorkspace(ctx, f.admin.Token, "elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	err = f.store.Update(ctx, func(w WriteStore) error {
		for i := 0; i <= 100; i++ {
			wid := ws.ID
			if i == 100 {
				wid = f.context.WorkspaceID
			}
			c := WorkContext{ID: fmt.Sprintf("candidate-%03d", i), WorkspaceID: wid, Name: "candidate", CreatedAt: *f.now}
			if e := w.PutContext(c); e != nil {
				return e
			}
			if e := w.PutGrant(Grant{ID: id(), SessionID: target.Session.ID, ContextID: c.ID, IssuerSessionID: f.admin.Session.ID, Permissions: PermissionWrite, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}); e != nil {
				return e
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.GrantContext(ctx, issuer.Token, GrantInput{SessionID: target.Session.ID, ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
}
