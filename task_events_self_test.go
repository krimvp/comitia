package comitia

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestExcludeSelfSkipsOwnTaskEventsPerInstance(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	worker, _ := delegate(t, f, f.admin.Token, "worker", f.context.ID, PermissionRead|PermissionWrite|PermissionClaim, time.Hour)
	inst := "worker-1"
	page := func(token string, instance *string) map[string]bool {
		t.Helper()
		p, err := f.svc.ReplayMessagesPage(ctx, token, f.context.ID, 0, 100, MessageFilters{ExcludeSelf: true, AgentInstance: instance, Schemas: []string{TaskEventSchema}})
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, m := range p.Messages {
			var e TaskEventPayload
			if err := json.Unmarshal(m.Envelope.Payload, &e); err != nil {
				t.Fatal(err)
			}
			seen[m.Envelope.TaskID+"/"+e.Kind] = true
		}
		return seen
	}
	mk := func(title string) Task {
		t.Helper()
		task, err := f.svc.CreateTask(ctx, f.admin.Token, f.context.ID, TaskInput{Title: title})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	claim := func(token string, task Task, instance ...AgentInstance) TaskClaim {
		t.Helper()
		c, err := f.svc.ClaimTask(ctx, token, task.ID, time.Minute, instance...)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	// The instance-configured worker claims, renews, completes and fails; an
	// admin releases and cancels two other leases the worker holds.
	a, b, c, d := mk("a"), mk("b"), mk("c"), mk("d")
	ca := claim(worker.Token, a, AgentInstance{ID: inst, Label: "W"})
	ra, err := f.svc.RenewClaim(ctx, worker.Token, a.ID, ca.Token, ca.Task.LeaseVersion, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CompleteTask(ctx, worker.Token, a.ID, ra.Token, ra.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	cb := claim(worker.Token, b, AgentInstance{ID: inst})
	if _, err := f.svc.FailTask(ctx, worker.Token, b.ID, TaskOutcomeInput{ClaimToken: cb.Token, LeaseVersion: cb.Task.LeaseVersion, Reason: "x"}); err != nil {
		t.Fatal(err)
	}
	cc := claim(worker.Token, c, AgentInstance{ID: inst})
	if _, err := f.svc.ReleaseTask(ctx, f.admin.Token, c.ID, "", cc.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	cd := claim(worker.Token, d, AgentInstance{ID: inst})
	if _, err := f.svc.CancelTask(ctx, f.admin.Token, d.ID, TaskOutcomeInput{LeaseVersion: cd.Task.LeaseVersion, Reason: "stop"}); err != nil {
		t.Fatal(err)
	}
	seen := page(worker.Token, &inst)
	for _, own := range []string{a.ID + "/claimed", a.ID + "/renewed", a.ID + "/completed", b.ID + "/claimed", b.ID + "/failed", c.ID + "/claimed", d.ID + "/claimed"} {
		if seen[own] {
			t.Fatalf("own event %s not excluded: %v", own, seen)
		}
	}
	for _, other := range []string{a.ID + "/created", c.ID + "/released", d.ID + "/cancelled"} {
		if !seen[other] {
			t.Fatalf("admin event %s excluded: %v", other, seen)
		}
	}
	other := "worker-2"
	if seen := page(worker.Token, &other); !seen[a.ID+"/claimed"] || !seen[a.ID+"/completed"] || !seen[b.ID+"/failed"] {
		t.Fatalf("another instance of the session lost events: %v", seen)
	}
	if seen := page(f.admin.Token, nil); seen[c.ID+"/released"] || seen[d.ID+"/cancelled"] || seen[a.ID+"/created"] || !seen[a.ID+"/claimed"] {
		t.Fatalf("admin exclusion: %v", seen)
	}
	for _, m := range taskEvents(t, f) {
		if m.AgentInstance != nil && (m.SessionID != worker.Session.ID || m.AgentInstance.ID != inst) {
			t.Fatalf("event attributed to the wrong actor: %+v", m)
		}
	}
	// An instance-less worker's own events stay excluded without an instance.
	e := mk("e")
	ce := claim(worker.Token, e)
	if _, err := f.svc.ReleaseTask(ctx, worker.Token, e.ID, ce.Token, ce.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	if seen := page(worker.Token, nil); seen[e.ID+"/claimed"] || seen[e.ID+"/released"] || !seen[e.ID+"/created"] || !seen[a.ID+"/claimed"] {
		t.Fatalf("instance-less exclusion: %v", seen)
	}
	// Attribution adds no presence beyond the instances that called.
	rows, err := f.svc.ListInstances(ctx, f.admin.Token, f.context.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("presence %+v %v", rows, err)
	}
	for _, row := range rows {
		if row.SessionID != worker.Session.ID || row.ID != inst && row.ID != other {
			t.Fatalf("unexpected presence %+v", row)
		}
	}
}
