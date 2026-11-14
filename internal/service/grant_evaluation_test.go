package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/sqlstore"

	"github.com/krimvp/comitia/internal/model"
)

type grantQueryCounts struct{ auth, sessions, grants, archives int }
type grantCountingQuery struct {
	sqlstore.Querier
	counts *grantQueryCounts
}

func (q grantCountingQuery) count(query string) {
	switch {
	case strings.Contains(query, "FROM sessions WHERE token_hash"):
		q.counts.auth++
	case strings.Contains(query, "FROM sessions WHERE id"):
		q.counts.sessions++
	case strings.Contains(query, "FROM grants WHERE"):
		q.counts.grants++
	case strings.Contains(query, "LEFT JOIN context_archives"):
		q.counts.archives++
	}
}
func (q grantCountingQuery) Query(query string, args ...any) (*sql.Rows, error) {
	q.count(query)
	return q.Querier.Query(query, args...)
}
func (q grantCountingQuery) QueryRow(query string, args ...any) *sql.Row {
	q.count(query)
	return q.Querier.QueryRow(query, args...)
}

type grantCountingStore struct {
	model.Store
	counts *grantQueryCounts
}

func (s grantCountingStore) View(ctx context.Context, fn func(model.ReadStore) error) error {
	return s.Store.View(ctx, func(r model.ReadStore) error {
		return fn(sqlstore.NewTxStore(grantCountingQuery{sqlstore.TxQuerier(r.(*sqlstore.TxStore)), s.counts}))
	})
}

func TestGrantAndArchiveShareTransactionEvaluation(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	var children []model.WorkContext
	for i := 0; i < 12; i++ {
		children = append(children, childContext(t, f, f.admin.Token, f.context.ID))
	}
	counts := new(grantQueryCounts)
	st := grantCountingStore{f.store, counts}
	if err := st.View(context.Background(), func(r model.ReadStore) error {
		for _, c := range children {
			gs, err := r.Grants(f.admin.Session.ID, c.ID, *f.now)
			if err != nil {
				return err
			}
			if len(gs) != 1 {
				t.Fatalf("grants=%d", len(gs))
			}
			at, err := r.ContextArchivedAt(c.ID, *f.now)
			if err != nil {
				return err
			}
			if at != nil {
				t.Fatal("active context archived")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("shared evaluation: %+v", *counts)
	if counts.sessions > 1 || counts.grants > 13 {
		t.Fatalf("shared ancestry was reloaded: %+v", *counts)
	}
}

func TestListContextsAuthenticatesOnceAndBatchesPolicyReads(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	for i := 0; i < 12; i++ {
		childContext(t, f, f.admin.Token, f.context.ID)
	}
	counts := new(grantQueryCounts)
	calls := 0
	svc, err := New(model.Config{Store: grantCountingStore{f.store, counts}, Clock: func() time.Time { calls++; return *f.now }})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.ListContexts(context.Background(), f.admin.Token, "", 100)
	if err != nil || len(got) != 13 {
		t.Fatalf("contexts=%d err=%v", len(got), err)
	}
	t.Logf("listing: queries=%+v clock=%d", *counts, calls)
	if counts.auth != 1 || calls != 1 || counts.grants > 1 || counts.archives > 1 || counts.sessions > 1 {
		t.Fatalf("repeated list work: queries=%+v clock=%d", *counts, calls)
	}
}

func TestDeepGrantFanInEvaluatesEachRecordOnce(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	// Deeper than new delegations may go: written as an older binary could have.
	cred := legacyChain(t, f, 40, true)
	leaf := model.Credential{Session: model.Session{ID: model.NewID(), PrincipalID: cred.Session.PrincipalID, ParentID: cred.Session.ID, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}}
	if err := f.store.Update(ctx, func(w model.WriteStore) error {
		parents, err := w.Grants(cred.Session.ID, f.context.ID, *f.now)
		if err != nil || len(parents) != 1 {
			return fmt.Errorf("chain grant: %d %v", len(parents), err)
		}
		if err = w.PutSession(leaf.Session, tokenHash(token())); err != nil {
			return err
		}
		for i := 0; i < 40; i++ {
			if err = w.PutGrant(model.Grant{ID: model.NewID(), SessionID: leaf.Session.ID, ContextID: f.context.ID, ParentGrantID: parents[0].ID, IssuerSessionID: cred.Session.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(time.Hour), CreatedAt: *f.now}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	counts := new(grantQueryCounts)
	if err := (grantCountingStore{f.store, counts}).View(ctx, func(r model.ReadStore) error {
		gs, err := r.Grants(leaf.Session.ID, f.context.ID, *f.now)
		if err != nil {
			return err
		}
		if len(gs) != 40 {
			t.Fatalf("grants=%d", len(gs))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("deep fan-in: %+v", *counts)
	if counts.grants > 42 || counts.sessions > 42 {
		t.Fatalf("fan-in revisited shared ancestry: %+v", *counts)
	}
}

type legacyGrantRead struct{ model.ReadStore }
type legacyGrantStore struct{ model.Store }

func (s legacyGrantStore) View(ctx context.Context, fn func(model.ReadStore) error) error {
	return s.Store.View(ctx, func(r model.ReadStore) error { return fn(legacyGrantRead{r}) })
}

func TestContextBatchAndFallbackKeepEffectiveAccess(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	reader, gs := delegate(t, f, f.admin.Token, "reader", f.context.ID, model.PermissionRead, time.Hour)
	c := childContext(t, f, f.admin.Token, f.context.ID)
	_, err := f.svc.GrantContext(ctx, f.admin.Token, model.GrantInput{SessionID: reader.Session.ID, ContextID: c.ID, Permissions: model.PermissionRead, ExpiresAt: f.now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ArchiveContext(ctx, f.admin.Token, c.ID); err != nil {
		t.Fatal(err)
	}
	for _, st := range []model.Store{f.store, legacyGrantStore{f.store}} {
		calls := 0
		svc, err := New(model.Config{Store: st, Clock: func() time.Time { calls++; return *f.now }})
		if err != nil {
			t.Fatal(err)
		}
		got, err := svc.ListContexts(ctx, reader.Token, "", 100)
		if err != nil || len(got) != 2 {
			t.Fatalf("contexts=%+v err=%v", got, err)
		}
		for _, item := range got {
			if item.ID == c.ID && !item.Archived {
				t.Fatal("missing archive metadata")
			}
		}
		if calls != 1 {
			t.Fatalf("clock calls=%d", calls)
		}
		children, err := svc.ListChildContexts(ctx, reader.Token, f.context.ID, "", 100)
		if err != nil || len(children) != 1 || !children[0].Archived {
			t.Fatalf("children=%+v err=%v", children, err)
		}
	}
	if err := f.svc.RevokeGrant(ctx, f.admin.Token, gs.ID); err != nil {
		t.Fatal(err)
	}
	for _, st := range []model.Store{f.store, legacyGrantStore{f.store}} {
		svc, _ := New(model.Config{Store: st, Clock: func() time.Time { return *f.now }})
		got, err := svc.ListContexts(ctx, reader.Token, "", 100)
		if err != nil || len(got) != 1 || got[0].ID != c.ID {
			t.Fatalf("stale authorization: %+v err=%v", got, err)
		}
	}
}

func TestListChildContextsBatchesArchiveMetadata(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	for i := 0; i < 12; i++ {
		childContext(t, f, f.admin.Token, f.context.ID)
	}
	counts := new(grantQueryCounts)
	calls := 0
	svc, _ := New(model.Config{Store: grantCountingStore{f.store, counts}, Clock: func() time.Time { calls++; return *f.now }})
	got, err := svc.ListChildContexts(context.Background(), f.admin.Token, f.context.ID, "", 100)
	if err != nil || len(got) != 12 {
		t.Fatalf("children=%d err=%v", len(got), err)
	}
	t.Logf("child listing: queries=%+v clock=%d", *counts, calls)
	if counts.auth != 1 || calls != 1 || counts.archives != 1 || counts.sessions > 1 || counts.grants > 2 {
		t.Fatalf("repeated child list work: %+v clock=%d", *counts, calls)
	}
}
