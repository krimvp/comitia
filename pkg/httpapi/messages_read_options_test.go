package httpapi_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/httpapi"
	"github.com/krimvp/comitia/pkg/httpapi/internal/legacy"
)

func TestMessageReadOptionsUpgradeOrders(t *testing.T) {
	for _, newServer := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-server", true: "new-server"}[newServer], func(t *testing.T) {
			ctx := context.Background()
			st, err := comitia.OpenSQLite(":memory:")
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
			m, err := svc.AppendMessage(ctx, owner.Token, wc.ID, comitia.MessageInput{DedupeKey: "own", Body: "own", AgentInstance: &comitia.AgentInstance{ID: "reader"}})
			if err != nil {
				t.Fatal(err)
			}
			if err = svc.AckMessages(ctx, owner.Token, wc.ID, m.Sequence, "reader"); err != nil {
				t.Fatal(err)
			}
			handler := legacy.NewHandler(svc)
			if newServer {
				handler = httpapi.NewHandler(svc)
			}
			ts := httptest.NewServer(handler)
			defer ts.Close()
			defaults, err := comitia.NewInstanceDefaults(&comitia.AgentInstance{ID: "reader"})
			if err != nil {
				t.Fatal(err)
			}
			client, err := httpapi.NewClientWithInstanceDefaults(ts.URL, ts.Client(), defaults)
			if err != nil {
				t.Fatal(err)
			}
			// The original request shape remains valid in each upgrade order.
			oldClient, err := legacy.NewClientWithInstanceDefaults(ts.URL, ts.Client(), comitia.InstanceDefaults{})
			if err != nil {
				t.Fatal(err)
			}
			for _, api := range []comitia.MessageAPI{client, oldClient} {
				for _, filters := range []comitia.MessageFilters{{}, {Schemas: []string{}}} {
					messages, err := api.ReplayMessages(ctx, owner.Token, wc.ID, 0, 100, filters)
					if err != nil || len(messages) != 1 {
						t.Fatalf("ordinary replay: %v %v", messages, err)
					}
				}
			}
			for _, tc := range []struct {
				name    string
				filters comitia.MessageFilters
				count   int
			}{
				{"exclude_self", comitia.MessageFilters{ExcludeSelf: true}, 0},
				{"from_cursor", comitia.MessageFilters{FromCursor: true}, 0},
				{"schemas", comitia.MessageFilters{Schemas: []string{"comitia.discussion.v1"}}, 0},
			} {
				for _, mode := range []string{"replay", "wait", "replay-page", "wait-page"} {
					var count int
					var err error
					switch mode {
					case "replay":
						var m []comitia.Message
						m, err = client.ReplayMessages(ctx, owner.Token, wc.ID, 0, 100, tc.filters)
						count = len(m)
					case "wait":
						var m []comitia.Message
						m, err = client.WaitMessages(ctx, owner.Token, wc.ID, 0, 0, tc.filters)
						count = len(m)
					case "replay-page":
						var p comitia.MessagePage
						p, err = client.ReplayMessagesPage(ctx, owner.Token, wc.ID, 0, 100, tc.filters)
						count = len(p.Messages)
						if newServer && tc.filters.FromCursor && p.NextAfter != 1 {
							t.Fatalf("resolved cursor missing: %+v", p)
						}
					case "wait-page":
						var p comitia.MessagePage
						p, err = client.WaitMessagesPage(ctx, owner.Token, wc.ID, 0, 0, tc.filters)
						count = len(p.Messages)
						if newServer && tc.filters.FromCursor && p.NextAfter != 1 {
							t.Fatalf("resolved cursor missing: %+v", p)
						}
					}
					if newServer {
						if err != nil || count != tc.count {
							t.Fatalf("%s %s: count=%d %v", tc.name, mode, count, err)
						}
					} else if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), tc.name+" unsupported by this server") {
						t.Fatalf("%s %s silently ignored: %v", tc.name, mode, err)
					}
				}
			}
		})
	}
}
