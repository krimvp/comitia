package httpapi_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/httpapi/internal/legacy"
)

func TestDedupeConflictDetailsAcrossUpgradeOrders(t *testing.T) {
	for _, newServer := range []bool{false, true} {
		for _, newClient := range []bool{false, true} {
			t.Run(map[bool]string{false: "old", true: "new"}[newClient]+"-client_"+map[bool]string{false: "old", true: "new"}[newServer]+"-handler", func(t *testing.T) {
				ctx := context.Background()
				store, err := comitia.OpenSQLite(":memory:")
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "boot"})
				if err != nil {
					t.Fatal(err)
				}
				_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "boot", AdminName: "owner", SessionTTL: time.Hour})
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
				handler := legacy.NewHandler(svc)
				if newServer {
					handler = httpapi.NewHandler(svc)
				}
				server := httptest.NewServer(handler)
				defer server.Close()
				var client comitia.API
				if newClient {
					client, err = httpapi.NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
				} else {
					client, err = legacy.NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
				}
				if err != nil {
					t.Fatal(err)
				}
				original, err := client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "started", Body: "first"})
				if err != nil {
					t.Fatal(err)
				}
				_, err = client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "started", Body: "second"})
				if !errors.Is(err, comitia.ErrConflict) || !strings.Contains(err.Error(), original.ID) {
					t.Fatalf("conflict: %v", err)
				}
				var conflict *comitia.DedupeConflictError
				if newClient && (!errors.As(err, &conflict) || conflict.MessageID != original.ID || conflict.Sequence != original.Sequence || strings.Join(conflict.Differs, ",") != "body") {
					t.Fatalf("typed conflict: %+v %v", conflict, err)
				}
				_, err = client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: strings.Repeat("k", comitia.MaxDedupeKeyBytes+1), Body: "x"})
				if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "at most 256 bytes") {
					t.Fatalf("long key: %v", err)
				}
			})
		}
	}
}
