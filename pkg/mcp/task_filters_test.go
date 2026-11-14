package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

func TestTaskFilterTools(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(strconv.FormatBool(remote), func(t *testing.T) {
			s, svc, owner, c := contextServer(t, remote)
			ctx := context.Background()
			s.Credential = owner.Token
			create := func(title string) comitia.Task {
				t.Helper()
				task, err := svc.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: title})
				if err != nil {
					t.Fatal(err)
				}
				return task
			}
			create("pending")
			live := create("live")
			other := create("other")
			expired := create("expired")
			for _, tc := range []struct {
				task     comitia.Task
				instance string
			}{{live, "mine"}, {other, "other"}, {expired, "mine"}} {
				if _, err := svc.ClaimTask(ctx, owner.Token, tc.task.ID, time.Hour, comitia.AgentInstance{ID: tc.instance}); err != nil {
					t.Fatal(err)
				}
			}
			// Revoke a delegated holder to exercise recoverable expiry without sleeps.
			_, worker, _, err := svc.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "worker", TTL: time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionRead | comitia.PermissionClaim, ExpiresAt: time.Now().Add(30 * time.Minute)}}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = svc.ReleaseTask(ctx, owner.Token, expired.ID, "", 1); err != nil {
				t.Fatal(err)
			}
			if _, err = svc.ClaimTask(ctx, worker.Token, expired.ID, time.Hour); err != nil {
				t.Fatal(err)
			}
			if err = svc.RevokeSession(ctx, owner.Token, worker.Session.ID); err != nil {
				t.Fatal(err)
			}
			defaults, err := comitia.NewInstanceDefaults(&comitia.AgentInstance{ID: "mine"})
			if err != nil {
				t.Fatal(err)
			}
			s.InstanceDefaults = defaults
			raw, _ := json.Marshal(map[string]any{"task_id": expired.ID})
			v, err := s.call(ctx, "get_task", raw)
			if err != nil || !v.(comitia.Task).LeaseExpired {
				t.Fatalf("get=%v %v", v, err)
			}
			for _, tc := range []struct {
				args  map[string]any
				count int
			}{{map[string]any{"claimable": true}, 2}, {map[string]any{"mine": true}, 1}, {map[string]any{"owner_session": owner.Session.ID}, 2}, {map[string]any{"owner_instance": "other"}, 1}, {map[string]any{"mine": true, "owner_instance": "other"}, 1}, {map[string]any{"owner_session": "missing"}, 0}} {
				tc.args["context_id"] = c.ID
				for _, page := range []bool{false, true} {
					tc.args["page"] = page
					raw, _ := json.Marshal(tc.args)
					v, err := s.call(ctx, "list_tasks", raw)
					if err != nil {
						t.Fatal(err)
					}
					var rows []comitia.Task
					if page {
						rows = v.(comitia.TaskPage).Tasks
					} else {
						rows = v.([]comitia.Task)
					}
					if len(rows) != tc.count {
						t.Fatalf("args=%v got=%v", tc.args, rows)
					}
				}
			}
			// Null optional values use the ordinary list and remain schema-valid.
			raw, _ = json.Marshal(map[string]any{"context_id": c.ID, "claimable": nil, "owner_session": nil, "owner_instance": nil, "mine": nil})
			v, err = s.call(ctx, "list_tasks", raw)
			if err != nil || len(v.([]comitia.Task)) != 4 {
				t.Fatalf("null=%v %v", v, err)
			}
			s.API = legacyDiscoveryAPI{s.API}
			raw, _ = json.Marshal(map[string]any{"context_id": c.ID, "mine": true})
			if _, err = s.call(ctx, "list_tasks", raw); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatal(err)
			}
		})
	}
}
