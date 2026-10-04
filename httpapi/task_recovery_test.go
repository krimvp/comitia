package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

type lostTaskResponse struct {
	next http.RoundTripper
	drop bool
}

func (d *lostTaskResponse) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := d.next.RoundTrip(r)
	if err == nil && d.drop {
		d.drop = false
		response.Body.Close()
		response.Body = io.NopCloser(failedTaskBody{})
	}
	return response, err
}

type failedTaskBody struct{}

func (failedTaskBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestHTTPTaskRecoveryAfterLostResponses(t *testing.T) {
	st, err := comitia.OpenSQLite(t.TempDir() + "/tasks.db")
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
	transport := &lostTaskResponse{next: ts.Client().Transport}
	client, err := NewClient(ts.URL, &http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	in := comitia.TaskInput{Title: "job", OperationID: "create"}
	transport.drop = true
	if got, err := client.CreateTask(ctx, owner.Token, c.ID, in); err == nil || got.ID != "" {
		t.Fatalf("lost create: %+v %v", got, err)
	}
	task, err := client.CreateTask(ctx, owner.Token, c.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	transport.drop = true
	if got, err := client.ClaimTaskWithOperation(ctx, owner.Token, task.ID, time.Minute, "claim"); err == nil || got.Token != "" {
		t.Fatalf("lost claim: %+v %v", got, err)
	}
	claim, err := client.ClaimTaskWithOperation(ctx, owner.Token, task.ID, time.Minute, "claim")
	if err != nil || claim.Task.LeaseVersion != 1 {
		t.Fatalf("recover claim: %+v %v", claim, err)
	}
	transport.drop = true
	if got, err := client.RenewClaimWithOperation(ctx, owner.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute, "renew"); err == nil || got.Token != "" {
		t.Fatalf("lost renew: %+v %v", got, err)
	}
	renewed, err := client.RenewClaimWithOperation(ctx, owner.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute, "renew")
	if err != nil || renewed.Task.LeaseVersion != 2 {
		t.Fatalf("recover renew: %+v %v", renewed, err)
	}
	again, err := client.RenewClaimWithOperation(ctx, owner.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute, "renew")
	if err != nil || !reflect.DeepEqual(again, renewed) {
		t.Fatalf("renew replay: %+v %v", again, err)
	}
	tasks, err := client.ListTasks(ctx, owner.Token, c.ID, "", 10)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("duplicate create: %+v %v", tasks, err)
	}
	msg := comitia.MessageInput{DedupeKey: "result", Body: "done", Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", TaskID: task.ID, Payload: json.RawMessage(`{}`)}, TaskFence: &comitia.TaskFence{TaskID: task.ID, ClaimToken: renewed.Token, LeaseVersion: renewed.Task.LeaseVersion}}
	accepted, err := client.AppendMessage(ctx, owner.Token, c.ID, msg)
	if err != nil || accepted.TaskAttempt == nil {
		t.Fatalf("HTTP fence: %+v %v", accepted, err)
	}
	if _, err = client.CompleteTask(ctx, owner.Token, task.ID, renewed.Token, renewed.Task.LeaseVersion); err != nil {
		t.Fatal(err)
	}
	msg.DedupeKey = "stale"
	if _, err = client.AppendMessage(ctx, owner.Token, c.ID, msg); !errors.Is(err, comitia.ErrStaleClaim) {
		t.Fatalf("HTTP stale result: %v", err)
	}
}
