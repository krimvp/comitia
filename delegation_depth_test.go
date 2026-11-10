package comitia

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// legacyChain writes a delegation chain directly, as an older binary without
// the depth limit could have. With nest, each grant's owner is a child session
// of the previous owner; otherwise one session holds the ancestry and a
// sibling session holds only the deepest grant.
func legacyChain(t *testing.T, f fixture, depth int, nest bool) Credential {
	t.Helper()
	var c Credential
	err := f.store.Update(context.Background(), func(w WriteStore) error {
		roots, err := w.Grants(f.admin.Session.ID, f.context.ID, *f.now)
		if err != nil || len(roots) == 0 {
			return fmt.Errorf("root grant: %v", err)
		}
		parent, issuer := roots[0], f.admin.Session.ID
		for i := 0; i < depth; i++ {
			if i == 0 || i == depth-1 || nest {
				p := Principal{ID: id(), Name: fmt.Sprint("legacy", i), CreatedAt: *f.now}
				tok := token()
				c = Credential{Token: tok, Session: Session{ID: id(), PrincipalID: p.ID, ParentID: issuer, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}}
				if err = w.PutPrincipal(p); err != nil {
					return err
				}
				if err = w.PutSession(c.Session, tokenHash(tok)); err != nil {
					return err
				}
			}
			g := Grant{ID: id(), SessionID: c.Session.ID, ContextID: f.context.ID, ParentGrantID: parent.ID, IssuerSessionID: issuer, Permissions: PermissionRead | PermissionGrant, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}
			if err = w.PutGrant(g); err != nil {
				return err
			}
			parent, issuer = g, c.Session.ID
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func depthLimited(err error) bool {
	return errors.Is(err, ErrInvalid) && strings.Contains(err.Error(), fmt.Sprint(maxDelegationDepth))
}

func TestDelegationDepthLimitRefusesOnlyNewLinks(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	peer, _ := delegate(t, f, f.admin.Token, "peer", f.context.ID, PermissionRead, time.Hour)
	perms := PermissionRead | PermissionGrant
	creds := []Credential{f.admin}
	for i := 1; i <= maxDelegationDepth; i++ {
		c, _ := delegate(t, f, creds[i-1].Token, fmt.Sprint("d", i), f.context.ID, perms, time.Hour)
		creds = append(creds, c)
	}
	leaf := creds[maxDelegationDepth]
	_, _, _, err := f.svc.DelegateSession(ctx, leaf.Token, DelegateInput{PrincipalName: "too-deep", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour)}}})
	if !depthLimited(err) {
		t.Fatalf("delegate past limit: %v", err)
	}
	in := GrantInput{SessionID: peer.Session.ID, ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour)}
	if _, err = f.svc.GrantContext(ctx, leaf.Token, in); !depthLimited(err) {
		t.Fatalf("grant past limit: %v", err)
	}
	if _, err = f.svc.GrantContext(ctx, creds[maxDelegationDepth-1].Token, in); err != nil {
		t.Fatalf("grant at limit: %v", err)
	}
	if _, err = f.svc.ListParticipants(ctx, leaf.Token, f.context.ID, 100); err != nil {
		t.Fatalf("leaf at limit lost access: %v", err)
	}

	// Chains already deeper than the limit keep evaluating; only extending them fails.
	deep := legacyChain(t, f, 2*maxDelegationDepth, true)
	if _, err = f.svc.ListParticipants(ctx, deep.Token, f.context.ID, 100); err != nil {
		t.Fatalf("legacy deep chain lost access: %v", err)
	}
	if _, _, _, err = f.svc.DelegateSession(ctx, deep.Token, DelegateInput{PrincipalName: "x", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour)}}}); !depthLimited(err) || !strings.Contains(err.Error(), "session") {
		t.Fatalf("legacy session chain extended: %v", err)
	}
	// A shallow session holding a deep grant chain is limited by the grant chain.
	holder := legacyChain(t, f, 2*maxDelegationDepth, false)
	if _, _, _, err = f.svc.DelegateSession(ctx, holder.Token, DelegateInput{PrincipalName: "x", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour)}}}); !depthLimited(err) || !strings.Contains(err.Error(), "grant") {
		t.Fatalf("legacy grant chain extended by delegation: %v", err)
	}
	if _, err = f.svc.GrantContext(ctx, holder.Token, in); !depthLimited(err) {
		t.Fatalf("legacy grant chain extended by grant: %v", err)
	}
	// Another covering grant with room left is used instead.
	if _, err = f.svc.GrantContext(ctx, f.admin.Token, GrantInput{SessionID: holder.Session.ID, ContextID: f.context.ID, Permissions: perms, ExpiresAt: f.now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.svc.GrantContext(ctx, holder.Token, in); err != nil {
		t.Fatalf("shallow source not used: %v", err)
	}
}
