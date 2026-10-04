package comitia

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPrivateTaskIDs(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: "private"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.CreatePrincipal(ctx, f.admin.Token, "outsider")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := f.svc.MintSession(ctx, f.admin.Token, p.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ops := map[string]func(string, string) error{
		"get":      func(raw, id string) error { _, e := f.svc.GetTask(ctx, raw, id); return e },
		"claim":    func(raw, id string) error { _, e := f.svc.ClaimTask(ctx, raw, id, time.Minute); return e },
		"renew":    func(raw, id string) error { _, e := f.svc.RenewClaim(ctx, raw, id, "claim", 1, time.Minute); return e },
		"complete": func(raw, id string) error { _, e := f.svc.CompleteTask(ctx, raw, id, "claim", 1); return e },
		"release":  func(raw, id string) error { _, e := f.svc.ReleaseTask(ctx, raw, id, "claim", 1); return e },
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			for _, id := range []string{task.ID, "missing"} {
				for _, raw := range []string{"", "invalid"} {
					if err := op(raw, id); !errors.Is(err, ErrUnauthenticated) {
						t.Errorf("unauthenticated %q: %v", id, err)
					}
				}
				if err := op(outsider.Token, id); err != ErrForbidden {
					t.Errorf("outsider %q: %v", id, err)
				}
			}
		})
	}
	if _, err := f.svc.GetTask(ctx, f.admin.Token, task.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPrivateGrantAndSessionIDs(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	p, err := f.svc.CreatePrincipal(ctx, f.admin.Token, "outsider")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := f.svc.MintSession(ctx, f.admin.Token, p.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := f.svc.GrantContext(ctx, f.admin.Token, GrantInput{SessionID: f.admin.Session.ID, ContextID: f.context.ID, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "invalid", outsider.Token} {
		want := ErrUnauthenticated
		if raw == outsider.Token {
			want = ErrForbidden
		}
		for _, id := range []string{grant.ID, "missing"} {
			if err := f.svc.RevokeGrant(ctx, raw, id); err != want {
				t.Errorf("grant %q: %v want %v", id, err, want)
			}
		}
		for _, id := range []string{f.admin.Session.ID, "missing"} {
			if err := f.svc.RevokeSession(ctx, raw, id); err != want {
				t.Errorf("session %q: %v want %v", id, err, want)
			}
		}
		for _, cid := range []string{f.context.ID, "missing"} {
			for _, sid := range []string{f.admin.Session.ID, "missing"} {
				_, err := f.svc.GrantContext(ctx, raw, GrantInput{SessionID: sid, ContextID: cid, Permissions: PermissionRead, ExpiresAt: f.now.Add(time.Minute)})
				if err != want {
					t.Errorf("grant context %q session %q: %v want %v", cid, sid, err, want)
				}
			}
		}
	}
	if err := f.svc.RevokeSession(ctx, f.admin.Token, "missing"); err != ErrNotFound {
		t.Fatalf("authorized missing: %v", err)
	}
}
