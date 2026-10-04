package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
)

type fakeAPI struct{ comitia.API }

func (fakeAPI) CreateWorkspace(_ context.Context, token, name string) (comitia.Workspace, error) {
	if token != "admin" {
		return comitia.Workspace{}, comitia.ErrForbidden
	}
	return comitia.Workspace{ID: "w1", Name: name}, nil
}

func (fakeAPI) ReplayMessages(_ context.Context, token, contextID string, after int64, limit int, _ ...comitia.MessageFilters) ([]comitia.Message, error) {
	if token != "reader" || contextID != "allowed" {
		return nil, comitia.ErrForbidden
	}
	return []comitia.Message{{ID: "m1", ContextID: contextID, Sequence: after + 1}}, nil
}

func TestClientRoundTripAndScopeRejection(t *testing.T) {
	ts := httptest.NewServer(NewHandler(fakeAPI{}))
	defer ts.Close()
	c, err := NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	w, err := c.CreateWorkspace(context.Background(), "admin", "demo")
	if err != nil || w.ID != "w1" || w.Name != "demo" {
		t.Fatalf("workspace=%+v err=%v", w, err)
	}
	_, err = c.ReplayMessages(context.Background(), "reader", "outside-grant", 0, 10)
	if !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("want forbidden, got %v", err)
	}
}

func TestStrictProtocolAndAuthentication(t *testing.T) {
	h := NewHandler(fakeAPI{})
	tests := []struct {
		name, method, auth, body string
		status                   int
	}{
		{"missing bearer", http.MethodPost, "", `{}`, http.StatusUnauthorized},
		{"wrong method", http.MethodGet, "Bearer admin", `{}`, http.StatusMethodNotAllowed},
		{"unknown field", http.MethodPost, "Bearer admin", `{"surprise":true}`, http.StatusBadRequest},
		{"multiple values", http.MethodPost, "Bearer admin", `{} {}`, http.StatusBadRequest},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/v1/workspaces/create", strings.NewReader(tc.body))
			r.Header.Set("Authorization", tc.auth)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d body %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestBootstrapIsOfflineOnly(t *testing.T) {
	ts := httptest.NewServer(NewHandler(fakeAPI{}))
	defer ts.Close()
	r, err := http.Post(ts.URL+"/v1/bootstrap", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without bearer status=%d", r.StatusCode)
	}
	c, _ := NewClient(ts.URL, ts.Client())
	_, _, err = c.Bootstrap(context.Background(), comitia.BootstrapInput{})
	if !errors.Is(err, ErrBootstrapOffline) {
		t.Fatalf("got %v", err)
	}
}

func TestSQLiteServiceRemoteScopedParity(t *testing.T) {
	ctx := context.Background()
	store, err := sqlite.OpenSQLite(t.TempDir() + "/acceptance.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "offline-secret"})
	if err != nil {
		t.Fatal(err)
	}
	_, admin, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "offline-secret", AdminName: "admin", SessionTTL: 4 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := svc.CreateWorkspace(ctx, admin.Token, "acceptance")
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := svc.CreateContext(ctx, admin.Token, comitia.CreateContextInput{WorkspaceID: workspace.ID, Name: "allowed"})
	if err != nil {
		t.Fatal(err)
	}
	denied, err := svc.CreateContext(ctx, admin.Token, comitia.CreateContextInput{WorkspaceID: workspace.ID, Name: "denied"})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().UTC().Add(time.Hour)
	_, delegated, _, err := svc.DelegateSession(ctx, admin.Token, comitia.DelegateInput{
		PrincipalName: "worker", TTL: 2 * time.Hour,
		Grants: []comitia.DelegatedGrant{{ContextID: allowed.ID, Permissions: comitia.PermissionRead | comitia.PermissionWrite | comitia.PermissionClaim, ExpiresAt: expires}},
	})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(NewHandler(svc))
	defer ts.Close()
	remote, err := NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = remote.GetContext(ctx, delegated.Token, allowed.ID); err != nil {
		t.Fatalf("get allowed context: %v", err)
	}
	if _, err = remote.GetContext(ctx, delegated.Token, denied.ID); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("cross-context read: want forbidden, got %v", err)
	}
	if _, err = remote.AppendMessage(ctx, delegated.Token, denied.ID, comitia.MessageInput{DedupeKey: "cross", Body: "blocked"}); !errors.Is(err, comitia.ErrForbidden) {
		t.Fatalf("cross-context append: want forbidden, got %v", err)
	}

	message, err := remote.AppendMessage(ctx, delegated.Token, allowed.ID, comitia.MessageInput{DedupeKey: "one", Body: "durable"})
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := remote.ReplayMessages(ctx, delegated.Token, allowed.ID, 0, 10)
	if err != nil || len(replayed) != 1 || replayed[0].ID != message.ID {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	if err = remote.AckMessages(ctx, delegated.Token, allowed.ID, message.Sequence); err != nil {
		t.Fatal(err)
	}
	cursor, err := remote.MessageCursor(ctx, delegated.Token, allowed.ID)
	if err != nil || cursor != message.Sequence {
		t.Fatalf("cursor=%d want=%d err=%v", cursor, message.Sequence, err)
	}

	task, err := remote.CreateTask(ctx, delegated.Token, allowed.ID, comitia.TaskInput{Title: "ship", Body: "acceptance"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := remote.GetTask(ctx, delegated.Token, task.ID)
	if err != nil || got.ID != task.ID {
		t.Fatalf("task=%+v err=%v", got, err)
	}
	listed, err := remote.ListTasks(ctx, delegated.Token, allowed.ID, comitia.TaskPending, 10)
	if err != nil || len(listed) != 1 || listed[0].ID != task.ID {
		t.Fatalf("tasks=%+v err=%v", listed, err)
	}
	claim, err := remote.ClaimTask(ctx, delegated.Token, task.ID, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claim, err = remote.RenewClaim(ctx, delegated.Token, task.ID, claim.Token, claim.Task.LeaseVersion, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := remote.CompleteTask(ctx, delegated.Token, task.ID, claim.Token, claim.Task.LeaseVersion)
	if err != nil || completed.Status != comitia.TaskCompleted {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
}
