package httpapi

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

func TestEndpointRejectsMisplacedFields(t *testing.T) {
	h := NewHandler(fakeAPI{})
	for _, body := range []string{`{"name":"w","task_id":"misplaced"}`, `{"name":"w","grant_id":"misplaced"}`} {
		r := httptest.NewRequest("POST", "/v1/workspaces/create", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer admin")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(wireHeader, canonicalWire)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("accepted misplaced field: %d %s", w.Code, w.Body.String())
		}
	}
	// Unknown routes must be resolved before their body is decoded.
	r := httptest.NewRequest("POST", "/v1/unknown", strings.NewReader(`{"task_id":"x"} trailing`))
	r.Header.Set("Authorization", "Bearer admin")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set(wireHeader, canonicalWire)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatalf("unknown route: %d %s", w.Code, w.Body.String())
	}
}

type wireProbe struct {
	comitia.API
	limit int
	lease time.Duration
}

func (p *wireProbe) ListTasks(_ context.Context, _, _ string, _ comitia.TaskStatus, limit int) ([]comitia.Task, error) {
	p.limit = limit
	return nil, nil
}
func (p *wireProbe) ClaimTask(_ context.Context, _, _ string, lease time.Duration, _ ...comitia.AgentInstance) (comitia.TaskClaim, error) {
	p.lease = lease
	return comitia.TaskClaim{Task: comitia.Task{ID: "t"}, Token: "claim"}, nil
}
func TestHTTPWireDefaultsSecondsAndConflicts(t *testing.T) {
	p := new(wireProbe)
	h := NewHandler(p)
	cases := []struct {
		path, body string
		status     int
		limit      int
		lease      time.Duration
	}{
		{"tasks/list", `{"context_id":"c"}`, 200, 100, 0},
		{"tasks/list", `{"context_id":"c","limit":0}`, 400, 0, 0},
		{"tasks/claim", `{"task_id":"t"}`, 200, 0, 300 * time.Second},
		{"tasks/claim", `{"task_id":"t","lease_seconds":90000}`, 200, 0, 90000 * time.Second},
		{"tasks/claim", `{"task_id":"t","lease_seconds":12}`, 200, 0, 12 * time.Second},
		{"tasks/claim", `{"task_id":"t","ttl":12000000000}`, 200, 0, 12 * time.Second},
		{"tasks/claim", `{"task_id":"t","lease_seconds":0}`, 400, 0, 0},
		{"tasks/claim", `{"task_id":"t","lease_seconds":12,"ttl":12000000000}`, 400, 0, 0},
		{"tasks/claim", `{"task_id":"t","open_ended":true}`, 200, 0, comitia.OpenLease},
		{"tasks/claim", `{"task_id":"t","open_ended":true,"lease_seconds":12}`, 400, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.path+tc.body, func(t *testing.T) {
			p.limit, p.lease = 0, 0
			r := httptest.NewRequest("POST", "/v1/"+tc.path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer caller")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set(wireHeader, canonicalWire)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || p.limit != tc.limit || p.lease != tc.lease {
				t.Fatalf("status=%d limit=%d lease=%v body=%s", w.Code, p.limit, p.lease, w.Body.String())
			}
			if tc.path == "tasks/list" && tc.status == 200 && strings.TrimSpace(w.Body.String()) != `{"tasks":[]}` {
				t.Fatalf("empty list: %s", w.Body.String())
			}
			if tc.path == "tasks/claim" && tc.status == 200 {
				var result map[string]map[string]json.RawMessage
				if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
					t.Fatal(err)
				}
				if result["claim"]["task"] == nil || result["claim"]["token"] == nil {
					t.Fatalf("Go names leaked: %s", w.Body.String())
				}
			}
		})
	}
}

func TestListRoutesRejectUnrelatedUnionFields(t *testing.T) {
	h := NewHandler(emptyCollectionsAPI{})
	for _, path := range []string{"contexts/list", "contexts/children", "participants/list", "tasks/list", "instances/list", "messages/replay", "messages/wait"} {
		r := httptest.NewRequest("POST", "/v1/"+path, strings.NewReader(`{"context_id":"c","grant_id":"misplaced"}`))
		if path == "contexts/list" {
			r = httptest.NewRequest("POST", "/v1/"+path, strings.NewReader(`{"grant_id":"misplaced"}`))
		}
		r.Header.Set("Authorization", "Bearer caller")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set(wireHeader, canonicalWire)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 400 {
			t.Fatalf("%s accepted misplaced field: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func (p *wireProbe) RenewClaim(_ context.Context, _, _, _ string, version int64, _ time.Duration) (comitia.TaskClaim, error) {
	return comitia.TaskClaim{Task: comitia.Task{LeaseVersion: version}}, nil
}
func (p *wireProbe) CompleteTask(_ context.Context, _, _, _ string, version int64) (comitia.Task, error) {
	return comitia.Task{LeaseVersion: version}, nil
}
func (p *wireProbe) ReleaseTask(_ context.Context, _, _, _ string, version int64) (comitia.Task, error) {
	return comitia.Task{LeaseVersion: version}, nil
}
func TestHTTPTaskVersionPresence(t *testing.T) {
	h := NewHandler(new(wireProbe))
	for _, route := range []string{"tasks/renew", "tasks/complete", "tasks/release"} {
		for _, version := range []string{"", `,"lease_version":null`, `,"lease_version":0`} {
			r := httptest.NewRequest("POST", "/v1/"+route, strings.NewReader(`{"task_id":"task","claim_token":"claim"`+version+`}`))
			r.Header.Set("Authorization", "Bearer caller")
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set(wireHeader, canonicalWire)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			want := 400
			if version == `,"lease_version":0` {
				want = 200
			}
			if w.Code != want {
				t.Fatalf("%s %s: %d %s", route, version, w.Code, w.Body.String())
			}
		}
	}
}
