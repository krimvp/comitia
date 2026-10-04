package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
)

func TestHTTPClaimInstanceOpenLeaseAndRelease(t *testing.T) {
	st, err := sqlite.OpenSQLite(t.TempDir() + "/tasks.db")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "local"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "local", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w, err := svc.CreateWorkspace(ctx, owner.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	c, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: w.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewHandler(svc))
	defer ts.Close()
	client, err := NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	task, err := client.CreateTask(ctx, owner.Token, c.ID, comitia.TaskInput{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.ClaimTask(ctx, owner.Token, task.ID, time.Minute, comitia.AgentInstance{ID: "a"}, comitia.AgentInstance{ID: "b"}); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("two instances: %v", err)
	}
	if _, err = client.ClaimTask(ctx, owner.Token, task.ID, time.Minute, comitia.AgentInstance{ID: "bad id"}); !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("bad instance: %v", err)
	}
	want := comitia.AgentInstance{ID: "worker-1", Label: "HTTP worker"}
	claim, err := client.ClaimTask(ctx, owner.Token, task.ID, comitia.OpenLease, want)
	if err != nil || claim.Task.LeaseOwnerInstance == nil || *claim.Task.LeaseOwnerInstance != want || !claim.Task.LeaseExpiresAt.Equal(owner.Session.ExpiresAt) {
		t.Fatalf("claim: %+v %v", claim.Task, err)
	}
	listed, err := client.ListTasks(ctx, owner.Token, c.ID, comitia.TaskLeased, 10)
	if err != nil || len(listed) != 1 || listed[0].LeaseOwnerInstance == nil || *listed[0].LeaseOwnerInstance != want {
		t.Fatalf("list: %+v %v", listed, err)
	}
	released, err := client.ReleaseTask(ctx, owner.Token, task.ID, "", claim.Task.LeaseVersion)
	if err != nil || released.Status != comitia.TaskPending || released.LeaseOwnerInstance != nil {
		t.Fatalf("release: %+v %v", released, err)
	}
	if _, err = client.ReleaseTask(ctx, owner.Token, task.ID, "", released.LeaseVersion); !errors.Is(err, comitia.ErrConflict) {
		t.Fatalf("release pending: %v", err)
	}
}
