package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/httpapi/internal/legacy"
)

func TestHTTPTaskFiltersAndLeaseMetadata(t *testing.T) {
	svc, client, owner, c, now := administrationServer(t)
	ctx := context.Background()
	tasks := map[string]comitia.Task{}
	for _, name := range []string{"pending", "expired", "live", "other-instance"} {
		task, err := client.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: name})
		if err != nil {
			t.Fatal(err)
		}
		tasks[name] = task
	}
	_, worker, _, err := svc.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "worker", TTL: time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionRead | comitia.PermissionClaim, ExpiresAt: now.Add(time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ClaimTask(ctx, worker.Token, tasks["expired"].ID, time.Hour, comitia.AgentInstance{ID: "worker"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"live", "other-instance"} {
		if _, err = client.ClaimTask(ctx, owner.Token, tasks[name].ID, time.Hour, comitia.AgentInstance{ID: name}); err != nil {
			t.Fatal(err)
		}
	}
	if err = svc.RevokeSession(ctx, owner.Token, worker.Session.ID); err != nil {
		t.Fatal(err)
	}
	defaults, err := comitia.NewInstanceDefaults(&comitia.AgentInstance{ID: "live"})
	if err != nil {
		t.Fatal(err)
	}
	client.InstanceDefaults = defaults
	got, err := client.GetTask(ctx, owner.Token, tasks["expired"].ID)
	if err != nil || !got.LeaseExpired || got.Status != comitia.TaskLeased {
		t.Fatalf("get=%+v %v", got, err)
	}

	upgraded := httptest.NewServer(NewHandler(svc))
	defer upgraded.Close()
	oldClient, err := legacy.NewClientWithInstanceDefaults(upgraded.URL, upgraded.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	oldTask, err := oldClient.GetTask(ctx, owner.Token, tasks["expired"].ID)
	if err != nil || oldTask.ID != tasks["expired"].ID || oldTask.Status != comitia.TaskLeased {
		t.Fatalf("old client expired task=%+v %v", oldTask, err)
	}
	oldList, err := oldClient.ListTasks(ctx, owner.Token, c.ID, comitia.TaskLeased, 10)
	if err != nil || len(oldList) != 3 {
		t.Fatalf("old client list=%v %v", oldList, err)
	}
	for _, tc := range []struct {
		f     comitia.TaskFilters
		count int
	}{{comitia.TaskFilters{Claimable: true}, 2}, {comitia.TaskFilters{Mine: true}, 1}, {comitia.TaskFilters{OwnerSession: owner.Session.ID}, 2}, {comitia.TaskFilters{OwnerInstance: "other-instance"}, 1}, {comitia.TaskFilters{OwnerSession: "missing"}, 0}} {
		rows, err := client.ListTasksFiltered(ctx, owner.Token, c.ID, "", tc.f, 10)
		if err != nil || len(rows) != tc.count {
			t.Fatalf("%+v: rows=%v %v", tc.f, rows, err)
		}
		after := ""
		count := 0
		for {
			page, err := client.ListTasksPageFiltered(ctx, owner.Token, c.ID, "", tc.f, after, 1)
			if err != nil {
				t.Fatal(err)
			}
			count += len(page.Tasks)
			if !page.HasMore {
				break
			}
			after = page.NextAfter
		}
		if count != tc.count {
			t.Fatalf("page count=%d want=%d", count, tc.count)
		}
	}
	for _, format := range []string{"", canonicalWire} {
		for _, endpoint := range []string{"get", "list", "page"} {
			body := `{"context_id":"` + c.ID + `","claimable":true}`
			path := "list"
			key := "tasks"
			if endpoint == "get" {
				body = `{"task_id":"` + tasks["expired"].ID + `"}`
				path = "get"
				key = "task"
			}
			if endpoint == "page" {
				body = `{"context_id":"` + c.ID + `","claimable":true,"page":true}`
				key = "task_page"
			}
			req := httptest.NewRequest("POST", "/v1/tasks/"+path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+owner.Token)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(wireHeader, format)
			w := httptest.NewRecorder()
			NewHandler(svc).ServeHTTP(w, req)
			var response map[string]json.RawMessage
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || w.Code != 200 {
				t.Fatalf("wire=%s %d %v", w.Body.String(), w.Code, err)
			}
			if !strings.Contains(string(response[key]), `"lease_expired":true`) {
				t.Fatalf("metadata missing: %s", w.Body.String())
			}
		}
	}
}

func TestHTTPTaskFiltersOldServerRejectsWithoutFallback(t *testing.T) {
	svc, _, owner, c, _ := administrationServer(t)
	ctx := context.Background()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; legacy.NewHandler(svc).ServeHTTP(w, r) }))
	defer server.Close()
	client, err := NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []comitia.TaskFilters{{Claimable: true}, {OwnerSession: owner.Session.ID}, {OwnerInstance: "worker"}, {Mine: true}} {
		for _, page := range []bool{false, true} {
			before := calls
			if page {
				_, err = client.ListTasksPageFiltered(ctx, owner.Token, c.ID, "", f, "", 1)
			} else {
				_, err = client.ListTasksFiltered(ctx, owner.Token, c.ID, "", f, 1)
			}
			if !errors.Is(err, ErrTaskFiltersUnsupported) || calls != before+1 {
				t.Fatalf("filter=%+v page=%v calls=%d err=%v", f, page, calls-before, err)
			}
		}
	}
	if _, err = client.ListTasksFiltered(ctx, owner.Token, c.ID, "", comitia.TaskFilters{}, 1); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPTaskFiltersLegacyAPI(t *testing.T) {
	svc, _, owner, c, _ := administrationServer(t)
	server := httptest.NewServer(NewHandler(legacyDiscoveryAPI{svc}))
	defer server.Close()
	client, err := NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListTasksFiltered(context.Background(), owner.Token, c.ID, "", comitia.TaskFilters{Mine: true}, 1)
	if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "unsupported") {
		t.Fatal(err)
	}
}
