package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
)

func TestMCPRecoverClaim(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			t.Setenv("COMITIA_AGENT_INSTANCE", "")
			t.Setenv("COMITIA_AGENT_INSTANCE_LABEL", "")
			s, svc, _, c := contextServer(t, remote)
			ctx := context.Background()
			task, err := svc.CreateTask(ctx, s.Credential, c.ID, comitia.TaskInput{Title: "job"})
			if err != nil {
				t.Fatal(err)
			}
			lost, err := svc.ClaimTask(ctx, s.Credential, task.ID, time.Minute, comitia.AgentInstance{ID: "worker"})
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range []map[string]any{{"task_id": task.ID}, {"lease_version": 1}, {"task_id": task.ID, "lease_version": 0}, {"task_id": task.ID, "lease_version": 1, "agent_instance": map[string]any{"id": ""}}, {"task_id": task.ID, "lease_version": 1, "agent_instance": map[string]any{"id": "worker"}, "lease_seconds": 60}} {
				raw, _ := json.Marshal(args)
				if _, err = s.call(ctx, "recover_claim", raw); !errors.Is(err, comitia.ErrInvalid) {
					t.Fatalf("invalid arguments: %v", err)
				}
			}
			raw, _ := json.Marshal(map[string]any{"task_id": task.ID, "lease_version": 1})
			if _, err = s.call(ctx, "recover_claim", raw); !errors.Is(err, comitia.ErrStaleClaim) {
				t.Fatalf("missing instance: %v", err)
			}
			defaults, err := comitia.NewInstanceDefaults(&comitia.AgentInstance{ID: "worker"})
			if err != nil {
				t.Fatal(err)
			}
			s.InstanceDefaults = defaults
			v, err := s.call(ctx, "recover_claim", raw)
			if err != nil {
				t.Fatal(err)
			}
			recovered := v.(comitia.TaskClaim)
			if recovered.Token == "" || recovered.Token == lost.Token || recovered.Task.LeaseVersion != lost.Task.LeaseVersion {
				t.Fatal("bad recovered claim")
			}
			explicit, _ := json.Marshal(map[string]any{"task_id": task.ID, "lease_version": 1, "agent_instance": map[string]any{"id": "other"}})
			if _, err = s.call(ctx, "recover_claim", explicit); !errors.Is(err, comitia.ErrStaleClaim) {
				t.Fatalf("explicit override: %v", err)
			}
			if _, err = svc.CompleteTask(ctx, s.Credential, task.ID, lost.Token, 1); !errors.Is(err, comitia.ErrStaleClaim) {
				t.Fatalf("lost token: %v", err)
			}
			if _, err = svc.CompleteTask(ctx, s.Credential, task.ID, recovered.Token, 1); err != nil {
				t.Fatal(err)
			}
			s.API = struct{ comitia.API }{s.API}
			if _, err = s.call(ctx, "recover_claim", raw); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("legacy API: %v", err)
			}
		})
	}
}

func TestRecoveredClaimCompactTokenInStructuredOnly(t *testing.T) {
	s, args := schemaConformanceFixture(t, "recover_claim")
	delete(args, "compact")
	result := resultCall(t, s, "recover_claim", args)
	if result["isError"] != false {
		t.Fatal("recover_claim failed")
	}
	claim := result["structuredContent"].(map[string]any)["result"].(comitia.TaskClaim)
	if claim.Token == "" || strings.Contains(resultText(result), claim.Token) {
		t.Fatal("compact result lost token or exposed it in text")
	}
}

func TestMCPRecoverClaimWithLegacyAPI(t *testing.T) {
	s, svc, _, c := contextServer(t, false)
	ctx := context.Background()
	task, err := svc.CreateTask(ctx, s.Credential, c.ID, comitia.TaskInput{Title: "job"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := svc.ClaimTask(ctx, s.Credential, task.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// An older API adapter still supports the original task tools.
	s.API = struct{ comitia.API }{svc}
	args := map[string]any{"task_id": task.ID, "lease_version": claim.Task.LeaseVersion}
	raw, _ := json.Marshal(args)
	if _, err = s.call(ctx, "recover_claim", raw); !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("legacy adapter: %v", err)
	}
	// A current MCP adapter also keeps an older backing API's rejection clear.
	ts := httptest.NewServer(httpapi.NewHandler(s.API))
	defer ts.Close()
	client, err := httpapi.NewClientWithInstanceDefaults(ts.URL, ts.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	s.API = client
	if _, err = s.call(ctx, "recover_claim", raw); !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("legacy remote adapter: %v", err)
	}
	args["claim_token"] = claim.Token
	raw, _ = json.Marshal(args)
	if _, err = s.call(ctx, "complete_task", raw); err != nil {
		t.Fatalf("legacy tool completion: %v", err)
	}
}
