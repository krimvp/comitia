package httpapi

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
)

func TestRemoteFilteredScanProgress(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "test"})
	if err != nil {
		t.Fatal(err)
	}
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "test", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, owner.Token, "test")
	if err != nil {
		t.Fatal(err)
	}
	wc, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewHandler(svc))
	defer ts.Close()
	client, err := NewClientWithInstanceDefaults(ts.URL, ts.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	after := int64(0)
	filter := comitia.MessageFilters{FromInstance: "sender"}
	for _, wait := range []bool{false, true} {
		t.Run(map[bool]string{false: "replay", true: "wait"}[wait], func(t *testing.T) {
			name := map[bool]string{false: "replay", true: "wait"}[wait]
			skipped, err := client.AppendMessage(ctx, owner.Token, wc.ID, comitia.MessageInput{DedupeKey: name + "-skip", Body: "skip"})
			if err != nil {
				t.Fatal(err)
			}
			var page comitia.MessagePage
			if wait {
				page, err = client.WaitMessagesPage(ctx, owner.Token, wc.ID, after, 0, filter)
			} else {
				page, err = client.ReplayMessagesPage(ctx, owner.Token, wc.ID, after, 100, filter)
			}
			if err != nil || len(page.Messages) != 0 || page.ScannedThrough != skipped.Sequence || page.NextAfter != after {
				t.Fatalf("remote scan: %+v %v", page, err)
			}
			m, err := client.AppendMessage(ctx, owner.Token, wc.ID, comitia.MessageInput{DedupeKey: map[bool]string{false: "replay", true: "wait"}[wait], Body: "ready", AgentInstance: &comitia.AgentInstance{ID: "sender"}})
			if err != nil {
				t.Fatal(err)
			}
			next, err := client.ReplayMessagesPage(ctx, owner.Token, wc.ID, page.ScannedThrough, 100, filter)
			if err != nil || len(next.Messages) == 0 || next.Messages[len(next.Messages)-1].ID != m.ID {
				t.Fatalf("resume skipped append: %+v %v", next, err)
			}
			after = next.ScannedThrough
		})
	}
	if cursor, err := client.MessageCursor(ctx, owner.Token, wc.ID); err != nil || cursor != 0 {
		t.Fatalf("remote scan acknowledged: %d %v", cursor, err)
	}
}
