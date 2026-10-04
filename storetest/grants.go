// Package storetest provides reusable conformance checks for Store implementations.
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

// Grants checks effective grants, creator-derived archival and transaction
// visibility/rollback, including durable grant/session descendant revocation.
// open must return a fresh, empty Store for each subtest.
// The suite closes each Store. It does not cover message/task/presence semantics.
func Grants(t *testing.T, open func(*testing.T) comitia.Store) {
	t.Helper()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	type records struct {
		sessions   []comitia.Session
		root, leaf comitia.Grant
	}
	initial := func() records {
		r := records{root: comitia.Grant{ID: "root", SessionID: "ancestor", IssuerSessionID: "issuer", ContextID: "parent", Permissions: comitia.PermissionAll, ExpiresAt: now.Add(time.Hour), CreatedAt: now}, leaf: comitia.Grant{ID: "leaf", SessionID: "owner", IssuerSessionID: "issuer", ContextID: "child", ParentGrantID: "root", Permissions: comitia.PermissionRead, ExpiresAt: now.Add(2 * time.Hour), CreatedAt: now}}
		for _, id := range []string{"ancestor", "issuer", "owner"} {
			r.sessions = append(r.sessions, comitia.Session{ID: id, PrincipalID: "principal", ExpiresAt: now.Add(3 * time.Hour), CreatedAt: now})
		}
		return r
	}
	seed := func(t *testing.T, st comitia.Store, r records) {
		t.Helper()
		err := st.Update(context.Background(), func(w comitia.WriteStore) error {
			if err := w.PutPrincipal(comitia.Principal{ID: "principal", Name: "test", CreatedAt: now}); err != nil {
				return err
			}
			for _, s := range r.sessions {
				if err := w.PutSession(s, "hash-"+s.ID); err != nil {
					return err
				}
			}
			if err := w.PutWorkspace(comitia.Workspace{ID: "workspace", Name: "test", CreatedAt: now}); err != nil {
				return err
			}
			for _, c := range []comitia.WorkContext{{ID: "parent", WorkspaceID: "workspace", Name: "parent", CreatedAt: now}, {ID: "child", WorkspaceID: "workspace", Name: "child", ParentContextID: "parent", CreatorSessionID: "owner", CreatedAt: now}} {
				if err := w.PutContext(c); err != nil {
					return err
				}
			}
			if err := w.PutGrant(r.root); err != nil {
				return err
			}
			if err := w.PutGrant(r.leaf); err != nil {
				return err
			}
			return w.PutContextCreator("child", "leaf", "root")
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check := func(t *testing.T, r comitia.ReadStore, at time.Time, active bool, end *time.Time) {
		t.Helper()
		gs, err := r.Grants("owner", "child", at)
		if err != nil {
			t.Fatal(err)
		}
		if (len(gs) == 1) != active || len(gs) > 1 {
			t.Fatalf("effective grants=%+v; active=%t", gs, active)
		}
		archived, err := r.ContextArchivedAt("child", at)
		if err != nil {
			t.Fatal(err)
		}
		if (archived == nil) != (end == nil) || archived != nil && !archived.Equal(*end) {
			t.Fatalf("archive=%v; want=%v", archived, end)
		}
		if batch, ok := r.(comitia.BatchContextReadStore); ok {
			batched, err := batch.GrantsForContexts("owner", []string{"parent", "child"}, at)
			if err != nil {
				t.Fatal(err)
			}
			if len(batched["parent"]) != 0 || len(batched["child"]) != len(gs) {
				t.Fatalf("batch grants=%+v", batched)
			}
			times, err := batch.ContextArchiveTimes([]string{"parent", "child"}, at)
			if err != nil {
				t.Fatal(err)
			}
			if times["parent"] != nil || (times["child"] == nil) != (end == nil) || times["child"] != nil && !times["child"].Equal(*end) {
				t.Fatalf("batch archives=%+v", times)
			}
		}
	}
	boundary := now.Add(-time.Minute)
	future := now.Add(time.Minute)
	cases := []struct {
		name   string
		change func(*records)
		active bool
		end    *time.Time
	}{
		{name: "active", active: true},
		{name: "future revocation denies immediately", change: func(r *records) { r.root.RevokedAt = &future }},
		{name: "parent grant expiry", change: func(r *records) { r.root.ExpiresAt = now }, end: &now},
		{name: "leaf grant expiry", change: func(r *records) { r.leaf.ExpiresAt = now }, end: &now},
		{name: "ancestor owner expiry", change: func(r *records) { r.sessions[0].ExpiresAt = now }, end: &now},
		{name: "issuer expiry", change: func(r *records) { r.sessions[1].ExpiresAt = now }, end: &now},
		{name: "owner expiry", change: func(r *records) { r.sessions[2].ExpiresAt = now }, end: &now},
		{name: "parent grant revoked", change: func(r *records) { r.root.RevokedAt = &boundary }, end: &boundary},
		{name: "leaf grant revoked", change: func(r *records) { r.leaf.RevokedAt = &boundary }, end: &boundary},
		{name: "ancestor owner revoked", change: func(r *records) { r.sessions[0].RevokedAt = &boundary }, end: &boundary},
		{name: "issuer revoked", change: func(r *records) { r.sessions[1].RevokedAt = &boundary }, end: &boundary},
		{name: "owner revoked", change: func(r *records) { r.sessions[2].RevokedAt = &boundary }, end: &boundary},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := open(t)
			t.Cleanup(func() {
				if err := st.Close(); err != nil {
					t.Error(err)
				}
			})
			r := initial()
			if tc.change != nil {
				tc.change(&r)
			}
			seed(t, st, r)
			if err := st.View(context.Background(), func(r comitia.ReadStore) error { check(t, r, now, tc.active, tc.end); return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
	t.Run("time is rechecked in one snapshot", func(t *testing.T) {
		st := open(t)
		t.Cleanup(func() { st.Close() })
		seed(t, st, initial())
		if err := st.View(context.Background(), func(r comitia.ReadStore) error {
			check(t, r, now, true, nil)
			end := now.Add(time.Hour)
			check(t, r, end, false, &end)
			check(t, r, now, true, nil)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	for _, kind := range []string{"grant", "session"} {
		t.Run(kind+" mutation and rollback", func(t *testing.T) {
			st := open(t)
			t.Cleanup(func() { st.Close() })
			seed(t, st, initial())
			rollback := errors.New("rollback")
			mutate := func(w comitia.WriteStore) error {
				if kind == "grant" {
					return w.RevokeGrantTree("root", now)
				}
				return w.RevokeSessionTree("issuer", now)
			}
			err := st.Update(context.Background(), func(w comitia.WriteStore) error {
				check(t, w, now, true, nil)
				if err := mutate(w); err != nil {
					return err
				}
				check(t, w, now, false, &now)
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatalf("rollback=%v", err)
			}
			if err := st.View(context.Background(), func(r comitia.ReadStore) error { check(t, r, now, true, nil); return nil }); err != nil {
				t.Fatal(err)
			}
			if err := st.Update(context.Background(), func(w comitia.WriteStore) error {
				check(t, w, now, true, nil)
				if err := mutate(w); err != nil {
					return err
				}
				check(t, w, now, false, &now)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.View(context.Background(), func(r comitia.ReadStore) error { check(t, r, now, false, &now); return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, kind := range []string{"grant", "session"} {
		t.Run(kind+" durable descendants", func(t *testing.T) {
			st := open(t)
			t.Cleanup(func() { st.Close() })
			records := initial()
			if kind == "session" {
				records.sessions[2].ParentID = "issuer"
			}
			records.sessions = append(records.sessions, comitia.Session{ID: "grandchild", PrincipalID: "principal", ParentID: "owner", ExpiresAt: now.Add(3 * time.Hour), CreatedAt: now})
			seed(t, st, records)
			if err := st.Update(context.Background(), func(w comitia.WriteStore) error {
				g := records.leaf
				g.ID = "grandleaf"
				g.ParentGrantID = "leaf"
				if err := w.PutGrant(g); err != nil {
					return err
				}
				for _, sid := range []string{"owner", "grandchild"} {
					independent := comitia.Grant{ID: "independent-" + sid, SessionID: sid, IssuerSessionID: "ancestor", ContextID: "parent", Permissions: comitia.PermissionRead, ExpiresAt: now.Add(time.Hour), CreatedAt: now}
					if err := w.PutGrant(independent); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			verify := func(r comitia.ReadStore, revoked bool) {
				t.Helper()
				var stamps []*time.Time
				if kind == "grant" {
					for _, id := range []string{"root", "leaf", "grandleaf"} {
						g, err := r.Grant(id)
						if err != nil {
							t.Fatal(err)
						}
						stamps = append(stamps, g.RevokedAt)
					}
					g, err := r.Grant("independent-owner")
					if err != nil {
						t.Fatal(err)
					}
					if g.RevokedAt != nil {
						t.Fatal("unrelated grant revoked")
					}
				} else {
					for _, id := range []string{"issuer", "owner", "grandchild"} {
						ss, err := r.Session(id)
						if err != nil {
							t.Fatal(err)
						}
						stamps = append(stamps, ss.RevokedAt)
					}
					ss, err := r.Session("ancestor")
					if err != nil {
						t.Fatal(err)
					}
					if ss.RevokedAt != nil {
						t.Fatal("unrelated session revoked")
					}
				}
				for i, at := range stamps {
					if revoked && (at == nil || !at.Equal(now)) || !revoked && at != nil {
						t.Errorf("%s tree row %d: revoked_at=%v; revoked=%t", kind, i, at, revoked)
					}
				}
				for _, sid := range []string{"owner", "grandchild"} {
					gs, err := r.Grants(sid, "parent", now)
					if err != nil {
						t.Fatal(err)
					}
					want := 1
					if kind == "session" && revoked {
						want = 0
					}
					if len(gs) != want {
						t.Errorf("%s independent grant: got %d, want %d", sid, len(gs), want)
					}
				}
			}
			mutate := func(w comitia.WriteStore) error {
				if kind == "grant" {
					return w.RevokeGrantTree("root", now)
				}
				return w.RevokeSessionTree("issuer", now)
			}
			rollback := errors.New("rollback descendants")
			err := st.Update(context.Background(), func(w comitia.WriteStore) error {
				verify(w, false)
				if err := mutate(w); err != nil {
					return err
				}
				verify(w, true)
				return rollback
			})
			if !errors.Is(err, rollback) {
				t.Fatalf("rollback=%v", err)
			}
			if err := st.View(context.Background(), func(r comitia.ReadStore) error { verify(r, false); return nil }); err != nil {
				t.Fatal(err)
			}
			if err := st.Update(context.Background(), func(w comitia.WriteStore) error {
				verify(w, false)
				if err := mutate(w); err != nil {
					return err
				}
				verify(w, true)
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if err := st.View(context.Background(), func(r comitia.ReadStore) error { verify(r, true); return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}

	t.Run("cycle rejected by both policies", func(t *testing.T) {
		st := open(t)
		t.Cleanup(func() { st.Close() })
		r := initial()
		r.root.ParentGrantID = "root"
		seed(t, st, r)
		if err := st.View(context.Background(), func(r comitia.ReadStore) error {
			gs, err := r.Grants("owner", "child", now)
			if err != nil || len(gs) != 0 {
				t.Fatalf("cycle grants=%+v err=%v", gs, err)
			}
			if _, err := r.ContextArchivedAt("child", now); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("cycle archive=%v", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("explicit archive preserves independent read", func(t *testing.T) {
		st := open(t)
		t.Cleanup(func() { st.Close() })
		seed(t, st, initial())
		if err := st.Update(context.Background(), func(w comitia.WriteStore) error {
			if err := w.PutContextArchive("child", boundary); err != nil {
				return err
			}
			check(t, w, now, true, &boundary)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("new independent grant after cached denial", func(t *testing.T) {
		st := open(t)
		t.Cleanup(func() { st.Close() })
		seed(t, st, initial())
		if err := st.Update(context.Background(), func(w comitia.WriteStore) error {
			check(t, w, now, true, nil)
			if err := w.RevokeGrantTree("root", now); err != nil {
				return err
			}
			check(t, w, now, false, &now)
			independent := initial().leaf
			independent.ID = "independent"
			independent.ParentGrantID = ""
			if err := w.PutGrant(independent); err != nil {
				return err
			}
			check(t, w, now, true, &now)
			if err := w.PutContextArchive("child", boundary); err != nil {
				return err
			}
			check(t, w, now, true, &boundary)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})

}
