package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func presenceBoundsFixture(t *testing.T) (*Service, *SQLiteStore, Credential, Credential, string, *time.Time) {
	t.Helper()
	st, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc, err := New(Config{Store: st, Clock: func() time.Time { return now }, BootstrapCredential: "local-secret"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, admin, err := svc.Bootstrap(ctx, BootstrapInput{Credential: "local-secret", AdminName: "root", SessionTTL: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, admin.Token, "test")
	if err != nil {
		t.Fatal(err)
	}
	wc, err := svc.CreateContext(ctx, admin.Token, CreateContextInput{WorkspaceID: ws.ID, Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	_, reader, _, err := svc.DelegateSession(ctx, admin.Token, DelegateInput{PrincipalName: "reader", TTL: time.Hour, Grants: []DelegatedGrant{{ContextID: wc.ID, Permissions: PermissionRead, ExpiresAt: now.Add(time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	return svc, st, admin, reader, wc.ID, &now
}

func sessionInstances(t *testing.T, svc *Service, token, cid, sid string) map[string]bool {
	t.Helper()
	rows, err := svc.ListInstances(context.Background(), token, cid)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, row := range rows {
		if row.SessionID == sid {
			out[row.ID] = true
		}
	}
	return out
}

func TestInstancePresenceEvictsOwnLeastRecentlySeen(t *testing.T) {
	svc, _, admin, reader, cid, now := presenceBoundsFixture(t)
	ctx := context.Background()
	if err := svc.SetInstanceResume(ctx, admin.Token, cid, &AgentInstance{ID: "admin-oldest"}, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxInstancesPerSession+2; i++ {
		*now = now.Add(time.Second)
		if err := svc.SetInstanceResume(ctx, reader.Token, cid, &AgentInstance{ID: fmt.Sprintf("w%03d", i)}, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	// A read heartbeat for a new instance succeeds and evicts the next oldest.
	*now = now.Add(time.Minute)
	heartbeat := "reader-heartbeat"
	if _, err := svc.ReplayMessages(ctx, reader.Token, cid, 0, 10, MessageFilters{AgentInstance: &heartbeat}); err != nil {
		t.Fatal(err)
	}
	rows := sessionInstances(t, svc, admin.Token, cid, reader.Session.ID)
	if len(rows) != MaxInstancesPerSession || rows["w000"] || rows["w001"] || rows["w002"] || !rows["w003"] || !rows[heartbeat] {
		t.Fatalf("reader rows (%d): %v", len(rows), rows)
	}
	if !sessionInstances(t, svc, admin.Token, cid, admin.Session.ID)["admin-oldest"] {
		t.Fatal("another session's row was evicted")
	}
}

func TestInstanceResumeRejectedInArchivedContext(t *testing.T) {
	svc, _, admin, reader, cid, _ := presenceBoundsFixture(t)
	ctx := context.Background()
	if err := svc.ArchiveContext(ctx, admin.Token, cid); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetInstanceResume(ctx, reader.Token, cid, &AgentInstance{ID: "late"}, json.RawMessage(`{}`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("archived resume: %v", err)
	}
	// Reads of archived contexts keep working; their heartbeat is bounded by the cap.
	instance := "reader"
	if _, err := svc.ReplayMessages(ctx, reader.Token, cid, 0, 10, MessageFilters{AgentInstance: &instance}); err != nil {
		t.Fatal(err)
	}
}

func TestInstancePresenceDropsEndedSessionsAfterRetention(t *testing.T) {
	svc, st, admin, reader, cid, now := presenceBoundsFixture(t)
	ctx := context.Background()
	for _, credential := range []Credential{admin, reader} {
		if err := svc.SetInstanceResume(ctx, credential.Token, cid, &AgentInstance{ID: "worker"}, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.RevokeSession(ctx, admin.Token, reader.Session.ID); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(InstanceRetention - time.Second)
	if rows := sessionInstances(t, svc, admin.Token, cid, reader.Session.ID); !rows["worker"] {
		t.Fatal("recently revoked row hidden")
	}
	*now = now.Add(2 * time.Second)
	if rows := sessionInstances(t, svc, admin.Token, cid, reader.Session.ID); len(rows) != 0 {
		t.Fatalf("revoked rows listed after retention: %v", rows)
	}
	count := func() (n int) {
		if err := st.db.QueryRow("SELECT COUNT(*) FROM instance_presence WHERE session_id=?", reader.Session.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 1 {
		t.Fatal("row deleted before a presence write")
	}
	if err := svc.SetInstanceResume(ctx, admin.Token, cid, &AgentInstance{ID: "worker"}, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatal("presence write did not prune the ended session's rows")
	}
}

func TestInstancePagesAreBoundedAndComplete(t *testing.T) {
	svc, _, admin, reader, cid, _ := presenceBoundsFixture(t)
	ctx := context.Background()
	want := map[string]bool{}
	for _, credential := range []Credential{admin, reader} {
		for i := 0; i < 3; i++ {
			id := fmt.Sprintf("i%d", i)
			if err := svc.SetInstanceResume(ctx, credential.Token, cid, &AgentInstance{ID: id}, json.RawMessage(`{}`)); err != nil {
				t.Fatal(err)
			}
			want[credential.Session.ID+"/"+id] = true
		}
	}
	seen := map[string]bool{}
	for after, pages := "", 0; ; pages++ {
		page, err := svc.ListInstancesPage(ctx, reader.Token, cid, after, 4)
		if err != nil || pages > 2 || len(page.Instances) > 4 {
			t.Fatalf("page: %+v %v", page, err)
		}
		for _, row := range page.Instances {
			key := row.SessionID + "/" + row.ID
			if seen[key] || row.Recent == nil {
				t.Fatalf("repeated or unannotated: %+v", row)
			}
			seen[key] = true
		}
		if after = page.NextAfter; !page.HasMore {
			break
		}
	}
	if len(seen) != len(want) {
		t.Fatalf("pages: %v", seen)
	}
	for _, after := range []string{"missing-separator", "s/bad id"} {
		if _, err := svc.ListInstancesPage(ctx, reader.Token, cid, after, 4); !errors.Is(err, ErrInvalid) {
			t.Fatalf("cursor %q: %v", after, err)
		}
	}
	if _, err := svc.ListInstancesPage(ctx, reader.Token, cid, "", 1001); !errors.Is(err, ErrInvalid) {
		t.Fatalf("limit: %v", err)
	}
}
