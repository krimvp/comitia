package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi/internal/legacy"
)

func TestHTTPRecoverClaim(t *testing.T) {
	svc, client, owner, c, _ := administrationServer(t)
	ctx := context.Background()
	task, err := client.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: "job"})
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := comitia.NewInstanceDefaults(&comitia.AgentInstance{ID: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	client.InstanceDefaults = defaults
	lost, err := client.ClaimTask(ctx, owner.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		version  int64
		instance []comitia.AgentInstance
		want     error
	}{{1, []comitia.AgentInstance{{ID: "other"}}, comitia.ErrStaleClaim}, {2, nil, comitia.ErrStaleClaim}, {0, nil, comitia.ErrInvalid}, {1, []comitia.AgentInstance{{}}, comitia.ErrInvalid}, {1, []comitia.AgentInstance{{ID: "a"}, {ID: "b"}}, comitia.ErrInvalid}} {
		got, e := client.RecoverClaim(ctx, owner.Token, task.ID, tc.version, tc.instance...)
		if !errors.Is(e, tc.want) || got.Token != "" {
			t.Fatalf("rejected request: %v", e)
		}
	}
	recovered, err := client.RecoverClaim(ctx, owner.Token, task.ID, 1)
	if err != nil || recovered.Token == "" || recovered.Token == lost.Token || !reflect.DeepEqual(lost.Task, recovered.Task) {
		t.Fatalf("recovery: %v", err)
	}
	if _, err = client.CompleteTask(ctx, owner.Token, task.ID, lost.Token, 1); !errors.Is(err, comitia.ErrStaleClaim) {
		t.Fatalf("lost token: %v", err)
	}
	if _, err = client.CompleteTask(ctx, owner.Token, task.ID, recovered.Token, 1); err != nil {
		t.Fatal(err)
	}
	if _, err = client.RecoverClaim(ctx, owner.Token, "missing", 1); !errors.Is(err, comitia.ErrForbidden) || errors.Is(err, ErrClaimRecoveryUnsupported) {
		t.Fatalf("missing task: %v", err)
	}

	// Both negotiated response names and legacy v1 names carry the claim.
	for _, canonical := range []bool{false, true} {
		task, err = svc.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: "wire"})
		if err != nil {
			t.Fatal(err)
		}
		lost, err = svc.ClaimTask(ctx, owner.Token, task.ID, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]any{"task_id": task.ID, "lease_version": lost.Task.LeaseVersion})
		req := httptest.NewRequest(http.MethodPost, "/v1/tasks/recover", bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+owner.Token)
		req.Header.Set("Content-Type", "application/json")
		if canonical {
			req.Header.Set(wireHeader, canonicalWire)
		}
		rec := httptest.NewRecorder()
		NewHandler(svc).ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("wire response: %d %s", rec.Code, rec.Body.String())
		}
		var result map[string]json.RawMessage
		if err = json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		var claim map[string]json.RawMessage
		if err = json.Unmarshal(result["claim"], &claim); err != nil {
			t.Fatal(err)
		}
		key := "Token"
		if canonical {
			key = "token"
		}
		if len(claim[key]) < 3 {
			t.Fatalf("claim token missing under %s", key)
		}
	}
}

func TestHTTPRecoveryUpgradeOrders(t *testing.T) {
	svc, _, owner, c, _ := administrationServer(t)
	ctx := context.Background()
	oldServer := httptest.NewServer(legacy.NewHandler(svc))
	defer oldServer.Close()
	newClient, err := NewClientWithInstanceDefaults(oldServer.URL, oldServer.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	task, err := newClient.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: "old"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := newClient.ClaimTask(ctx, owner.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := newClient.RecoverClaim(ctx, owner.Token, task.ID, claim.Task.LeaseVersion); !errors.Is(err, ErrClaimRecoveryUnsupported) || got.Token != "" {
		t.Fatalf("old server: %v", err)
	}
	// Rejected recovery did not mutate ownership or invalidate the original token.
	if _, err = newClient.CompleteTask(ctx, owner.Token, task.ID, claim.Token, claim.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	newServer := httptest.NewServer(NewHandler(svc))
	defer newServer.Close()
	oldClient, err := legacy.NewClientWithInstanceDefaults(newServer.URL, newServer.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	task, err = oldClient.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: "new"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err = oldClient.ClaimTask(ctx, owner.Token, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := NewClientWithInstanceDefaults(newServer.URL, newServer.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	claim, err = upgraded.RecoverClaim(ctx, owner.Token, task.ID, claim.Task.LeaseVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = oldClient.CompleteTask(ctx, owner.Token, task.ID, claim.Token, claim.Task.LeaseVersion); err != nil {
		t.Fatalf("old client recovered claim: %v", err)
	}
	unsupported := httptest.NewServer(NewHandler(legacyDiscoveryAPI{svc}))
	defer unsupported.Close()
	noAPI, err := NewClient(unsupported.URL, unsupported.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = noAPI.RecoverClaim(ctx, owner.Token, task.ID, 1); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("optional API: %v", err)
	}
}

func TestHTTPRecoveryMissingVersion(t *testing.T) {
	svc, _, owner, _, _ := administrationServer(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/tasks/recover", bytes.NewBufferString(`{"task_id":"missing"}`))
	req.Header.Set("Authorization", "Bearer "+owner.Token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewHandler(svc).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing version: %d", rec.Code)
	}
	raw, err := json.Marshal(recoverClaimRequest{TaskID: "t", LeaseVersion: ptr(int64(1))})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("claim_instance")) {
		t.Fatalf("optional field not omitted: %s", raw)
	}
}
