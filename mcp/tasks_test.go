package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

type claimAPI struct {
	comitia.API
	lease    time.Duration
	instance []comitia.AgentInstance
	token    string
	version  int64
}

func (f *claimAPI) ClaimTask(_ context.Context, _, id string, lease time.Duration, instance ...comitia.AgentInstance) (comitia.TaskClaim, error) {
	f.lease, f.instance = lease, instance
	return comitia.TaskClaim{Task: comitia.Task{ID: id}}, nil
}
func (f *claimAPI) RenewClaim(_ context.Context, _, id, _ string, _ int64, lease time.Duration) (comitia.TaskClaim, error) {
	f.lease = lease
	return comitia.TaskClaim{Task: comitia.Task{ID: id}}, nil
}
func (f *claimAPI) ReleaseTask(_ context.Context, _, id, token string, version int64) (comitia.Task, error) {
	f.token, f.version = token, version
	return comitia.Task{ID: id, Status: comitia.TaskPending}, nil
}

func TestClaimInstanceOpenLeaseAndRelease(t *testing.T) {
	f := &claimAPI{}
	s := &Server{API: f, Credential: "secret"}
	ctx := context.Background()
	if _, err := s.call(ctx, "claim_task", json.RawMessage(`{"task_id":"t1","open_ended":true,"agent_instance":{"id":"w-1","label":"worker"}}`)); err != nil || f.lease != comitia.OpenLease || len(f.instance) != 1 || f.instance[0] != (comitia.AgentInstance{ID: "w-1", Label: "worker"}) {
		t.Fatalf("claim: %v lease=%v instance=%v", err, f.lease, f.instance)
	}
	if _, err := s.call(ctx, "claim_task", json.RawMessage(`{"task_id":"t1"}`)); err != nil || f.lease != 300*time.Second || f.instance != nil {
		t.Fatalf("default claim: %v lease=%v instance=%v", err, f.lease, f.instance)
	}
	if _, err := s.call(ctx, "renew_task", json.RawMessage(`{"task_id":"t1","claim_token":"k","lease_version":1,"open_ended":true}`)); err != nil || f.lease != comitia.OpenLease {
		t.Fatalf("renew: %v lease=%v", err, f.lease)
	}
	for _, raw := range []string{`{"task_id":"t1","open_ended":true,"lease_seconds":60}`, `{"task_id":"t1","agent_instance":{"label":"x","pid":1}}`} {
		if _, err := s.call(ctx, "claim_task", json.RawMessage(raw)); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("raw=%s err=%v", raw, err)
		}
	}
	v, err := s.call(ctx, "release_task", json.RawMessage(`{"task_id":"t1","lease_version":4}`))
	if err != nil || v.(comitia.Task).Status != comitia.TaskPending || f.token != "" || f.version != 4 {
		t.Fatalf("release: %v %v token=%q version=%d", v, err, f.token, f.version)
	}
	if _, err = s.call(ctx, "release_task", json.RawMessage(`{"lease_version":4}`)); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("release without task: %v", err)
	}
}
