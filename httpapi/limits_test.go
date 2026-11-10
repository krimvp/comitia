package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
)

type remoteFixture struct {
	svc    *comitia.Service
	client *Client
	token  string
	cid    string
}

func newRemoteFixture(t *testing.T) remoteFixture {
	t.Helper()
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "boot"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "boot", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, owner.Token, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	wc, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "context"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewHandler(svc))
	t.Cleanup(ts.Close)
	client, err := NewClientWithInstanceDefaults(ts.URL, ts.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	return remoteFixture{svc, client, owner.Token, wc.ID}
}

// The worst JSON escaping (six bytes per input byte) of a maximal new message
// or task must still fit one remote response.
func TestMaximalEscapedContentFitsRemoteResponse(t *testing.T) {
	f := newRemoteFixture(t)
	ctx := context.Background()
	escaped := strings.Repeat("\x00", comitia.MaxMessageBodyBytes)
	if _, err := f.svc.AppendMessage(ctx, f.token, f.cid, comitia.MessageInput{DedupeKey: "max", Body: escaped}); err != nil {
		t.Fatal(err)
	}
	page, err := f.client.ReplayMessagesPage(ctx, f.token, f.cid, 0, 1, comitia.MessageFilters{MaxBytes: 1024})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Body != escaped {
		t.Fatalf("maximal message: n=%d err=%v", len(page.Messages), err)
	}
	task, err := f.svc.CreateTask(ctx, f.token, f.cid, comitia.TaskInput{Title: strings.Repeat("<", comitia.MaxTaskTitleBytes), Body: strings.Repeat("\x01", comitia.MaxTaskBodyBytes)})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.client.GetTask(ctx, f.token, task.ID); err != nil || got.Body != task.Body {
		t.Fatalf("maximal task: err=%v", err)
	}
	info, err := f.client.ServerInfo(ctx, f.token)
	if err != nil || info.Limits == nil || info.Limits.TaskBodyBytes != comitia.MaxTaskBodyBytes {
		t.Fatalf("remote server_info limits=%+v err=%v", info.Limits, err)
	}
}

func TestRemoteTaskListingOfLargeTasks(t *testing.T) {
	f := newRemoteFixture(t)
	ctx := context.Background()
	for i := 0; i < 20; i++ {
		if _, err := f.svc.CreateTask(ctx, f.token, f.cid, comitia.TaskInput{Title: "large", Body: strings.Repeat("b", comitia.MaxTaskBodyBytes)}); err != nil {
			t.Fatal(err)
		}
	}
	after, seen := "", 0
	for {
		page, err := f.client.ListTasksPage(ctx, f.token, f.cid, "", after, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Tasks) != 1 {
			t.Fatalf("page of %d maximal tasks", len(page.Tasks))
		}
		seen++
		if !page.HasMore {
			break
		}
		after = page.NextAfter
	}
	if seen != 20 {
		t.Fatalf("paged %d tasks", seen)
	}
	if _, err := f.client.ListTasks(ctx, f.token, f.cid, "", 1000); !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "lower limit") {
		t.Fatalf("oversized array: %v", err)
	}
}
