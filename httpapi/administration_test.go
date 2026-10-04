package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

func administrationServer(t *testing.T) (*comitia.Service, *Client, comitia.Credential, comitia.WorkContext, time.Time) {
	t.Helper()
	store, err := comitia.OpenSQLite(t.TempDir() + "/administration.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	svc, err := comitia.New(comitia.Config{Store: store, Clock: func() time.Time { return now }, BootstrapCredential: "offline"})
	if err != nil {
		t.Fatal(err)
	}
	_, owner, err := svc.Bootstrap(context.Background(), comitia.BootstrapInput{Credential: "offline", AdminName: "owner", SessionTTL: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewHandler(svc))
	t.Cleanup(server.Close)
	client, err := NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := client.CreateWorkspace(context.Background(), owner.Token, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	context, err := client.CreateContext(context.Background(), owner.Token, comitia.CreateContextInput{WorkspaceID: workspace.ID, Name: "context"})
	if err != nil {
		t.Fatal(err)
	}
	return svc, client, owner, context, now
}

func TestAdministrationClientServerConformance(t *testing.T) {
	svc, client, owner, c, now := administrationServer(t)
	ctx := context.Background()
	p, err := client.CreatePrincipal(ctx, owner.Token, "member")
	if err != nil || p.ID == "" || p.Name != "member" || p.Admin || !p.CreatedAt.Equal(now) {
		t.Fatalf("principal: %+v %v", p, err)
	}
	member, err := client.MintSession(ctx, owner.Token, p.ID, 90*time.Minute+500*time.Millisecond)
	if err != nil || member.Token == "" || member.Session.PrincipalID != p.ID || !member.Session.ExpiresAt.Equal(now.Add(90*time.Minute+500*time.Millisecond)) {
		t.Fatalf("mint: %+v %v", member.Session, err)
	}
	grant, err := client.GrantContext(ctx, owner.Token, comitia.GrantInput{SessionID: member.Session.ID, ContextID: c.ID, Permissions: comitia.PermissionRead | comitia.PermissionWrite | comitia.PermissionGrant, ExpiresAt: now.Add(time.Hour)})
	if err != nil || grant.ID == "" || grant.SessionID != member.Session.ID || grant.ContextID != c.ID || grant.Permissions != comitia.PermissionRead|comitia.PermissionWrite|comitia.PermissionGrant || !grant.ExpiresAt.Equal(now.Add(time.Hour)) || grant.IssuerSessionID != owner.Session.ID {
		t.Fatalf("grant: %+v %v", grant, err)
	}
	second, err := client.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: c.WorkspaceID, Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	name, helper, grants, err := client.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "helper", TTL: 2 * time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionRead | comitia.PermissionWrite, ExpiresAt: now.Add(time.Hour)}, {ContextID: second.ID, Permissions: comitia.PermissionRead, ExpiresAt: now.Add(30 * time.Minute)}}})
	if err != nil || name.Name != "helper" || helper.Session.PrincipalID != name.ID || helper.Session.ParentID != owner.Session.ID || !helper.Session.ExpiresAt.Equal(now.Add(2*time.Hour)) || len(grants) != 2 {
		t.Fatalf("delegate: %+v %+v %d %v", name, helper.Session, len(grants), err)
	}
	for i, g := range grants {
		want := []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionRead | comitia.PermissionWrite, ExpiresAt: now.Add(time.Hour)}, {ContextID: second.ID, Permissions: comitia.PermissionRead, ExpiresAt: now.Add(30 * time.Minute)}}[i]
		if g.SessionID != helper.Session.ID || g.ContextID != want.ContextID || g.Permissions != want.Permissions || !g.ExpiresAt.Equal(want.ExpiresAt) || g.ParentGrantID == "" {
			t.Fatalf("delegated grant: %+v", g)
		}
		if _, err := client.GetContext(ctx, helper.Token, g.ContextID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.CreatePrincipal(ctx, member.Token, "forbidden"); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("nonadmin create: %v", err)
	}
	if _, err := client.MintSession(ctx, member.Token, p.ID, time.Hour); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("nonadmin mint: %v", err)
	}
	if _, err := client.CreatePrincipal(ctx, "invalid", "x"); !errors.Is(err, comitia.ErrUnauthenticated) {
		t.Fatalf("invalid auth: %v", err)
	}
	if _, err := client.MintSession(ctx, owner.Token, p.ID, 0); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("invalid mint TTL: %v", err)
	}
	if _, _, _, err := client.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "invalid", TTL: time.Hour}); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("invalid delegate: %v", err)
	}
	if _, err := client.GrantContext(ctx, owner.Token, comitia.GrantInput{SessionID: member.Session.ID, ContextID: c.ID, Permissions: 0, ExpiresAt: now.Add(time.Hour)}); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("invalid permissions: %v", err)
	}
	for name, call := range map[string]func() error{
		"mint": func() error { _, err := client.MintSession(ctx, "invalid", p.ID, time.Hour); return err },
		"delegate": func() error {
			_, _, _, err := client.DelegateSession(ctx, "invalid", comitia.DelegateInput{PrincipalName: "x", TTL: time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionRead, ExpiresAt: now.Add(time.Hour)}}})
			return err
		},
		"revoke session": func() error { return client.RevokeSession(ctx, "invalid", helper.Session.ID) },
		"grant": func() error {
			_, err := client.GrantContext(ctx, "invalid", comitia.GrantInput{SessionID: member.Session.ID, ContextID: c.ID, Permissions: comitia.PermissionRead, ExpiresAt: now.Add(time.Hour)})
			return err
		},
		"revoke grant": func() error { return client.RevokeGrant(ctx, "invalid", grant.ID) },
	} {
		if err := call(); !errors.Is(err, comitia.ErrUnauthenticated) {
			t.Fatalf("%s invalid auth: %v", name, err)
		}
	}
	if err := client.RevokeSession(ctx, member.Token, helper.Session.ID); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("revoke unrelated session: %v", err)
	}
	if err := client.RevokeGrant(ctx, member.Token, grants[0].ID); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("revoke unrelated grant: %v", err)
	}
	if _, _, _, err := client.DelegateSession(ctx, member.Token, comitia.DelegateInput{PrincipalName: "escalate", TTL: time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionClaim, ExpiresAt: now.Add(time.Hour)}}}); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("delegate escalation: %v", err)
	}
	if _, err := client.GrantContext(ctx, member.Token, comitia.GrantInput{SessionID: helper.Session.ID, ContextID: c.ID, Permissions: comitia.PermissionClaim, ExpiresAt: now.Add(time.Hour)}); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("grant escalation: %v", err)
	}
	if err := client.RevokeGrant(ctx, owner.Token, grant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetContext(ctx, member.Token, c.ID); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("revoked grant still active: %v", err)
	}
	if err := client.RevokeSession(ctx, owner.Token, helper.Session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetContext(ctx, helper.Token, c.ID); !errors.Is(err, comitia.ErrUnauthenticated) {
		t.Fatalf("revoked session still active: %v", err)
	}
	if _, err := svc.GetContext(ctx, owner.Token, c.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCanonicalAndLegacyHTTPInputs(t *testing.T) {
	svc, client, owner, c, now := administrationServer(t)
	h := NewHandler(svc)
	p, err := client.CreatePrincipal(context.Background(), owner.Token, "mint-target")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		route, body string
		status      int
	}{
		{"principals/create", `{"name":1}`, 400},
		{"sessions/mint", `{"principal_id":"x","ttl_seconds":"30"}`, 400},
		{"sessions/delegate", `{"delegate":{"principal_name":"x","ttl_seconds":30,"grants":{}}}`, 400},
		{"sessions/revoke", `{"session_id":1}`, 400},
		{"grants/create", `{"grant":{"session_id":"x","context_id":"x","permissions":"read","expires_at":"2026-10-04T13:00:00Z"}}`, 400},
		{"grants/revoke", `{"grant_id":1}`, 400},
		{"contexts/create", `{"create_context":{"workspace_id":"` + c.WorkspaceID + `","name":"canonical"}}`, 200},
		{"contexts/create", `{"create_context":{"WorkspaceID":"` + c.WorkspaceID + `","Name":"legacy"}}`, 200},
		{"contexts/create", `{"create_context":{"workspace_id":"` + c.WorkspaceID + `","WorkspaceID":"same","name":"conflict"}}`, 400},
		{"contexts/create", `{"create_context":{"WorkspaceID":"` + c.WorkspaceID + `","Name":"x","task_id":"misplaced"}}`, 400},
		{"sessions/mint", `{"principal_id":"` + p.ID + `","ttl_seconds":30.5}`, 200},
		{"sessions/mint", `{"principal_id":"` + p.ID + `","ttl":30500000000}`, 200},
		{"sessions/mint", `{"principal_id":"` + p.ID + `","ttl_seconds":30,"ttl":30000000000}`, 400},
		{"sessions/mint", `{"principal_id":"` + p.ID + `","ttl_seconds":30,"ttl":null}`, 400},
		{"sessions/mint", `{"principal_id":"` + p.ID + `"}`, 400},
		{"sessions/mint", `{"principal_id":"` + p.ID + `","ttl_seconds":1e300}`, 400},
		{"sessions/delegate", `{"delegate":{"PrincipalName":"legacy","TTL":30000000000,"Grants":[{"ContextID":"` + c.ID + `","Permissions":1,"ExpiresAt":"` + now.Add(20*time.Second).Format(time.RFC3339) + `"}]}}`, 200},
		{"sessions/delegate", `{"delegate":{"principal_name":"conflict","ttl_seconds":30,"TTL":30000000000,"grants":[]}}`, 400},
		{"tasks/create", `{"context_id":"` + c.ID + `","task":{"title":"x","Title":"x"}}`, 400},
		{"tasks/create", `null`, 400},
		{"tasks/create", `{"context_id":"` + c.ID + `","task":{"title":"x","title":"y"}}`, 400},
	} {
		t.Run(tc.route+tc.body, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/"+tc.route, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer "+owner.Token)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("%d %s", w.Code, w.Body.String())
			}
			if tc.status == 200 {
				var out map[string]map[string]json.RawMessage
				// Delegate has an additional array result; inspect the entire raw body instead.
				if tc.route != "sessions/delegate" {
					if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
						t.Fatal(err)
					}
					for _, v := range out {
						if v["ID"] != nil || v["Session"] != nil {
							t.Fatalf("Go names leaked: %s", w.Body.String())
						}
					}
				}
			}
		})
	}
}

func TestReferenceHTTPRoundTripAndPermissions(t *testing.T) {
	_, client, owner, c, now := administrationServer(t)
	ctx := context.Background()
	input := comitia.AddReferenceInput{ContextID: c.ID, Kind: "issue", URI: "https://example.test/issues/55"}
	got, err := client.AddReference(ctx, owner.Token, input)
	if err != nil || got.ID == "" || got.ContextID != input.ContextID || got.Kind != input.Kind || got.URI != input.URI || !got.CreatedAt.Equal(now) {
		t.Fatalf("reference: %+v %v", got, err)
	}
	_, reader, _, err := client.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "reader", TTL: time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionRead, ExpiresAt: now.Add(time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.AddReference(ctx, reader.Token, input); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("read-only reference: %v", err)
	}
	if _, err := client.AddReference(ctx, "invalid", input); !errors.Is(err, comitia.ErrUnauthenticated) {
		t.Fatalf("invalid reference auth: %v", err)
	}
	input.Kind = ""
	if _, err := client.AddReference(ctx, owner.Token, input); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("missing reference kind: %v", err)
	}
	input.Kind = "issue"
	input.URI = ""
	if _, err := client.AddReference(ctx, owner.Token, input); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("invalid reference: %v", err)
	}
}

func TestClientSendsCanonicalWireDTOs(t *testing.T) {
	probe := new(wireProbe)
	handler := NewHandler(probe)
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		body = string(raw)
		r.Body = io.NopCloser(strings.NewReader(body))
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	client, err := NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ClaimTask(context.Background(), "caller", "task", 1250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if body != `{"task_id":"task","lease_seconds":1.25}` || probe.lease != 1250*time.Millisecond {
		t.Fatalf("wire=%s lease=%v", body, probe.lease)
	}
	if _, err := client.ClaimTask(context.Background(), "caller", "task", comitia.OpenLease); err != nil {
		t.Fatal(err)
	}
	if body != `{"task_id":"task","open_ended":true}` {
		t.Fatalf("open lease wire=%s", body)
	}
}
