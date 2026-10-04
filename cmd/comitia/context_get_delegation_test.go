package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
)

func TestContextGetAndAtomicMultiGrantDelegation(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded", true: "remote"}[remote], func(t *testing.T) {
			store, err := comitia.OpenSQLite(t.TempDir() + "/cli.db")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "once"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "once", AdminName: "owner", SessionTTL: 4 * time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			workspace, err := svc.CreateWorkspace(ctx, owner.Token, "workspace")
			if err != nil {
				t.Fatal(err)
			}
			first, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: workspace.ID, Name: "first"})
			if err != nil {
				t.Fatal(err)
			}
			second, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: workspace.ID, Name: "second"})
			if err != nil {
				t.Fatal(err)
			}
			var api comitia.API = svc
			if remote {
				server := httptest.NewServer(httpapi.NewHandler(svc))
				defer server.Close()
				api, err = httpapi.NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
				if err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			cfg := config{credential: owner.Token}
			if err := runContexts(ctx, cfg, api, json.NewEncoder(&output), "context-get", []string{"--context-id", first.ID}); err != nil {
				t.Fatal(err)
			}
			var got comitia.WorkContext
			if err := json.Unmarshal(output.Bytes(), &got); err != nil || got.ID != first.ID || got.Name != "first" {
				t.Fatalf("get: %+v %v", got, err)
			}
			output.Reset()
			if err := runSessions(ctx, cfg, api, json.NewEncoder(&output), "delegate", []string{"--name", "helper", "--ttl", "2h", "--grant", first.ID + ":read,write:30m", "--grant", second.ID + ":read:20m"}); err != nil {
				t.Fatal(err)
			}
			var delegation struct {
				Principal  comitia.Principal
				Credential comitia.Credential
				Grants     []comitia.Grant
			}
			if err := json.Unmarshal(output.Bytes(), &delegation); err != nil {
				t.Fatal(err)
			}
			if len(delegation.Grants) != 2 || delegation.Grants[0].ContextID != first.ID || delegation.Grants[1].ContextID != second.ID || delegation.Grants[0].Permissions != comitia.PermissionRead|comitia.PermissionWrite || delegation.Grants[1].Permissions != comitia.PermissionRead {
				t.Fatalf("delegation: %+v", delegation.Grants)
			}
			for _, id := range []string{first.ID, second.ID} {
				if _, err := api.GetContext(ctx, delegation.Credential.Token, id); err != nil {
					t.Fatal(err)
				}
			}
			cfg.credential = delegation.Credential.Token
			if err := runContexts(ctx, cfg, api, json.NewEncoder(&output), "context-get", nil); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("missing get ID: %v", err)
			}
			// Attenuation is atomic: an invalid second grant never produces a credential.
			output.Reset()
			if err := runSessions(ctx, cfg, api, json.NewEncoder(&output), "delegate", []string{"--grant", first.ID + ":read:10m", "--grant", second.ID + ":read,write:10m"}); !errors.Is(err, comitia.ErrForbidden) || output.Len() != 0 {
				t.Fatalf("invalid second grant: %v output=%s", err, output.String())
			}
			cfg.credential = owner.Token
			for _, args := range [][]string{{"--context-id", first.ID, "--grant", second.ID + ":read:10m"}, {"--grant", "malformed"}, {"--grant", first.ID + ":read:0s"}, {"--ttl", "10m", "--grant", first.ID + ":read:20m"}} {
				if err := runSessions(ctx, cfg, api, json.NewEncoder(&output), "delegate", args); !errors.Is(err, comitia.ErrInvalid) {
					t.Fatalf("invalid %v: %v", args, err)
				}
			}
		})
	}
}
