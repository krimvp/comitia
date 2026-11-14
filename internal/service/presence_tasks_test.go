package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/krimvp/comitia/internal/model"
	"github.com/krimvp/comitia/internal/sqlstore"
)

func TestPresenceRecency(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx, raw, cid := context.Background(), f.admin.Token, f.context.ID
	if err := f.svc.SetInstanceResume(ctx, raw, cid, &model.AgentInstance{ID: "worker"}, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	seen := *f.now
	for _, test := range []struct {
		age     time.Duration
		seconds int64
		recent  bool
	}{
		{-time.Second, 0, true}, {0, 0, true}, {89*time.Second + 999*time.Millisecond, 89, true}, {90 * time.Second, 90, false}, {10 * time.Minute, 600, false},
	} {
		*f.now = seen.Add(test.age)
		rows, err := f.svc.ListInstances(ctx, raw, cid)
		if err != nil || len(rows) != 1 {
			t.Fatalf("list: %v %v", rows, err)
		}
		p := rows[0]
		if p.SeenSecondsAgo == nil || *p.SeenSecondsAgo != test.seconds || p.Recent == nil || *p.Recent != test.recent || !p.Active || !p.LastSeen.Equal(seen) {
			t.Fatalf("age %v: %+v", test.age, p)
		}
	}
	if err := f.store.Update(ctx, func(w model.WriteStore) error {
		return w.PutInstancePresence(model.InstancePresence{ID: "worker", SessionID: f.admin.Session.ID, ContextID: cid})
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := f.svc.ListInstances(ctx, raw, cid)
	if err != nil || len(rows) != 1 || rows[0].SeenSecondsAgo != nil || rows[0].Recent != nil {
		t.Fatalf("unknown timestamp: %v %v", rows, err)
	}

}

func TestTaskPresenceLifecycle(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx, raw, cid := context.Background(), f.admin.Token, f.context.ID
	task, err := f.svc.CreateTask(ctx, raw, cid, model.TaskInput{Title: "work"})
	if err != nil {
		t.Fatal(err)
	}
	owner := model.AgentInstance{ID: "worker", Label: "Worker"}
	check := func(seen time.Time) {
		t.Helper()
		rows, err := f.svc.ListInstances(ctx, raw, cid)
		if err != nil || len(rows) != 1 {
			t.Fatalf("list: %v %v", rows, err)
		}
		p := rows[0]
		if p.ID != owner.ID || p.Label != owner.Label || p.SessionID != f.admin.Session.ID || !p.LastSeen.Equal(seen) {
			t.Fatalf("presence: %+v", p)
		}
	}
	claim, err := f.svc.ClaimTaskWithOperation(ctx, raw, task.ID, time.Hour, "claim", owner)
	if err != nil {
		t.Fatal(err)
	}
	check(*f.now)
	first := *f.now
	*f.now = f.now.Add(time.Second)
	claim, err = f.svc.ClaimTaskWithOperation(ctx, raw, task.ID, time.Hour, "claim", owner)
	if err != nil {
		t.Fatal(err)
	}
	check(first) // Successful retries still respect the heartbeat throttle.
	if err := f.svc.SetInstanceResume(ctx, raw, cid, &owner, []byte(`{"keep":true}`)); err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(model.InstanceHeartbeatInterval)
	claim, err = f.svc.ClaimTaskWithOperation(ctx, raw, task.ID, time.Hour, "claim", owner)
	if err != nil {
		t.Fatal(err)
	}
	check(*f.now)
	owner.Label = "Updated presence label"
	if err := f.svc.SetInstanceResume(ctx, raw, cid, &owner, []byte(`{"keep":true}`)); err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(model.InstanceHeartbeatInterval)
	renewToken, renewVersion := claim.Token, claim.Task.LeaseVersion
	claim, err = f.svc.RenewClaimWithOperation(ctx, raw, task.ID, renewToken, renewVersion, time.Hour, "renew")
	if err != nil {
		t.Fatal(err)
	}
	check(*f.now)
	*f.now = f.now.Add(model.InstanceHeartbeatInterval)
	claim, err = f.svc.RenewClaimWithOperation(ctx, raw, task.ID, renewToken, renewVersion, time.Hour, "renew")
	if err != nil {
		t.Fatal(err)
	}
	check(*f.now)
	last := *f.now
	*f.now = f.now.Add(model.InstanceHeartbeatInterval)
	if _, err := f.svc.CompleteTask(ctx, raw, task.ID, "invalid", claim.Task.LeaseVersion); !errors.Is(err, model.ErrStaleClaim) {
		t.Fatalf("stale: %v", err)
	}
	if _, err := f.svc.ClaimTask(ctx, raw, task.ID, time.Hour, model.AgentInstance{ID: "rejected"}); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("conflict: %v", err)
	}
	check(last)
	if _, err := f.svc.CompleteTask(ctx, raw, task.ID, claim.Token, claim.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	check(*f.now)
	rows, _ := f.svc.ListInstances(ctx, raw, cid)
	if string(rows[0].Resume) != `{"keep":true}` {
		t.Fatal("task activity changed resume")
	}
}

func TestTaskPresenceAnonymousAndFailures(t *testing.T) {
	for _, mode := range []string{"anonymous", "heartbeat-error", "sqlite-savepoint"} {
		t.Run(mode, func(t *testing.T) {
			f := setup(t, ":memory:")
			defer f.store.Close()
			ctx, raw, cid := context.Background(), f.admin.Token, f.context.ID
			var owner []model.AgentInstance
			if mode != "anonymous" {
				owner = []model.AgentInstance{{ID: "worker"}}
			}
			if mode == "heartbeat-error" {
				f.svc.store = &failingPresenceStore{Store: f.store}
			}
			if mode == "sqlite-savepoint" {
				if _, err := sqlstore.DB(f.store).Exec(`CREATE TRIGGER reject_presence AFTER INSERT ON instance_presence BEGIN SELECT RAISE(FAIL,'presence failed'); END;`); err != nil {
					t.Fatal(err)
				}
			}
			task, err := f.svc.CreateTask(ctx, raw, cid, model.TaskInput{Title: "work"})
			if err != nil {
				t.Fatal(err)
			}
			claim, err := f.svc.ClaimTask(ctx, raw, task.ID, time.Hour, owner...)
			if err != nil {
				t.Fatal(err)
			}
			*f.now = f.now.Add(model.InstanceHeartbeatInterval)
			claim, err = f.svc.RenewClaim(ctx, raw, task.ID, claim.Token, claim.Task.LeaseVersion, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			*f.now = f.now.Add(model.InstanceHeartbeatInterval)
			done, err := f.svc.CompleteTask(ctx, raw, task.ID, claim.Token, claim.Task.LeaseVersion)
			if err != nil || done.Status != model.TaskCompleted {
				t.Fatalf("complete: %v %v", done.Status, err)
			}
			rows, err := f.svc.ListInstances(ctx, raw, cid)
			if err != nil || len(rows) != 0 {
				t.Fatalf("unexpected presence: %v %v", rows, err)
			}
			events, err := f.svc.ReplayMessages(ctx, raw, cid, 0, 100, model.MessageFilters{Schema: model.TaskEventSchema})
			if err != nil || len(events) != 4 {
				t.Fatalf("task events: %d %v", len(events), err)
			}
		})
	}
}
