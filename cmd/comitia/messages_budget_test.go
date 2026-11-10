package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/sqlite"
)

func TestRemoteCLIReplayAndWaitPages(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "boot"})
	if err != nil {
		t.Fatal(err)
	}
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "boot", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, owner.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	wc, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, err = svc.AppendMessage(ctx, owner.Token, wc.ID, comitia.MessageInput{DedupeKey: fmt.Sprint(i), Body: strings.Repeat("x", 500000)})
		if err != nil {
			t.Fatal(err)
		}
	}
	ts := httptest.NewServer(httpapi.NewHandler(svc))
	defer ts.Close()
	client, err := httpapi.NewClientWithInstanceDefaults(ts.URL, ts.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{credential: owner.Token}
	for _, cmd := range []string{"replay", "wait"} {
		t.Run(cmd, func(t *testing.T) {
			var out bytes.Buffer
			args := append([]string{"--context-id", wc.ID, "--max-bytes", "4194304"}, zeroWait(cmd)...)
			if err := runMessages(ctx, cfg, client, json.NewEncoder(&out), cmd, args); err != nil {
				t.Fatal(err)
			}
			var legacy []comitia.Message
			if err := json.Unmarshal(out.Bytes(), &legacy); err != nil || len(legacy) != 3 {
				t.Fatalf("legacy remote %s count=%d: %v", cmd, len(legacy), err)
			}
			after := int64(0)
			for _, want := range []int{2, 1, 0} {
				out.Reset()
				args = append([]string{"--context-id", wc.ID, "--page", "--after", fmt.Sprint(after)}, zeroWait(cmd)...)
				if err := runMessages(ctx, cfg, client, json.NewEncoder(&out), cmd, args); err != nil {
					t.Fatal(err)
				}
				var page comitia.MessagePage
				if err := json.Unmarshal(out.Bytes(), &page); err != nil || len(page.Messages) != want || page.HasMore != (want == 2) || page.Messages == nil {
					t.Fatalf("%s page count=%d more=%v: %v", cmd, len(page.Messages), page.HasMore, err)
				}
				after = page.NextAfter
			}
		})
	}
}
