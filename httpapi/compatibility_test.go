package httpapi_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi"
	"github.com/krimvp/comitia/httpapi/internal/legacy"
)

// This is the review's genuine pre-PR Client/Handler probe, extended to cover
// renewal, administration, nested inputs, open leases and old empty results.
func TestV1UpgradeOrderCompatibility(t *testing.T) {
	for _, newServer := range []bool{false, true} {
		for _, newClient := range []bool{false, true} {
			name := map[bool]string{false: "old", true: "new"}[newClient] + "-client_" + map[bool]string{false: "old", true: "new"}[newServer] + "-handler"
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				store, err := comitia.OpenSQLite(":memory:")
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()
				now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
				svc, err := comitia.New(comitia.Config{Store: store, Clock: func() time.Time { return now }, BootstrapCredential: "boot"})
				if err != nil {
					t.Fatal(err)
				}
				_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "boot", AdminName: "owner", SessionTTL: 4 * time.Hour})
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
				workspace, err := client.CreateWorkspace(ctx, owner.Token, "workspace")
				if err != nil || workspace.ID == "" || workspace.Name != "workspace" || !workspace.CreatedAt.Equal(now) {
					t.Fatalf("workspace metadata lost: %+v %v", workspace, err)
				}
				parent, err := client.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: workspace.ID, Name: "parent"})
				if err != nil || parent.WorkspaceID != workspace.ID || parent.Name != "parent" {
					t.Fatalf("context metadata lost: %+v %v", parent, err)
				}
				// Older Handlers omit empty collection members. A new Client must still
				// produce the normalized public API result without rejecting the server.
				if newClient {
					tasks, err := client.ListTasks(ctx, owner.Token, parent.ID, "", 100)
					if err != nil || tasks == nil || len(tasks) != 0 {
						t.Fatalf("empty tasks: %v %v", tasks, err)
					}
					children, err := client.ListChildContexts(ctx, owner.Token, parent.ID, "", 100)
					if err != nil || children == nil || len(children) != 0 {
						t.Fatalf("empty children: %v %v", children, err)
					}
					instances, err := client.ListInstances(ctx, owner.Token, parent.ID)
					if err != nil || instances == nil || len(instances) != 0 {
						t.Fatalf("empty instances: %v %v", instances, err)
					}
					replay, err := client.ReplayMessages(ctx, owner.Token, parent.ID, 0, 100)
					if err != nil || replay == nil || len(replay) != 0 {
						t.Fatalf("empty replay: %v %v", replay, err)
					}
					wait, err := client.WaitMessages(ctx, owner.Token, parent.ID, 0, 0)
					if err != nil || wait == nil || len(wait) != 0 {
						t.Fatalf("empty wait: %v %v", wait, err)
					}
				}
				task, err := client.CreateTask(ctx, owner.Token, parent.ID, comitia.TaskInput{Title: "work", Body: "details"})
				if err != nil || task.ID == "" || task.ContextID != parent.ID || task.Title != "work" {
					t.Fatalf("create task: %+v %v", task, err)
				}
				claim, err := client.ClaimTask(ctx, owner.Token, task.ID, time.Minute, comitia.AgentInstance{ID: "worker"})
				if err != nil || claim.Task.LeaseVersion != 1 || claim.Task.LeaseOwnerSessionID != owner.Session.ID || claim.Task.LeaseExpiresAt == nil || !claim.Task.LeaseExpiresAt.Equal(now.Add(time.Minute)) || claim.Token == "" || claim.Task.LeaseTokenHash != "" {
					t.Fatalf("claim metadata lost: %+v %v", claim.Task, err)
				}
				renewed, err := client.RenewClaim(ctx, owner.Token, task.ID, claim.Token, claim.Task.LeaseVersion, 2*time.Minute)
				if err != nil || renewed.Task.LeaseVersion != 2 || renewed.Task.LeaseOwnerSessionID != owner.Session.ID || renewed.Task.LeaseExpiresAt == nil || !renewed.Task.LeaseExpiresAt.Equal(now.Add(2*time.Minute)) {
					t.Fatalf("renew: %+v %v", renewed.Task, err)
				}
				done, err := client.CompleteTask(ctx, owner.Token, task.ID, renewed.Token, renewed.Task.LeaseVersion)
				if err != nil || done.Status != comitia.TaskCompleted || done.ContextID != parent.ID {
					t.Fatalf("complete: %+v %v", done, err)
				}
				openTask, err := client.CreateTask(ctx, owner.Token, parent.ID, comitia.TaskInput{Title: "open"})
				if err != nil {
					t.Fatal(err)
				}
				open, err := client.ClaimTask(ctx, owner.Token, openTask.ID, comitia.OpenLease)
				if err != nil || open.Task.LeaseExpiresAt == nil || !open.Task.LeaseExpiresAt.Equal(owner.Session.ExpiresAt) {
					t.Fatalf("open lease: %+v %v", open.Task, err)
				}
				if _, err := client.ReleaseTask(ctx, owner.Token, openTask.ID, open.Token, open.Task.LeaseVersion); err != nil {
					t.Fatal(err)
				}
				principal, err := client.CreatePrincipal(ctx, owner.Token, "member")
				if err != nil {
					t.Fatal(err)
				}
				ttl := 90*time.Minute + time.Nanosecond
				credential, err := client.MintSession(ctx, owner.Token, principal.ID, ttl)
				if err != nil || credential.Token == "" || credential.Session.PrincipalID != principal.ID || !credential.Session.ExpiresAt.Equal(now.Add(ttl)) {
					t.Fatalf("mint metadata/TTL lost: %+v %v", credential.Session, err)
				}
				grant, err := client.GrantContext(ctx, owner.Token, comitia.GrantInput{SessionID: credential.Session.ID, ContextID: parent.ID, Permissions: comitia.PermissionRead | comitia.PermissionWrite, ExpiresAt: now.Add(time.Hour)})
				if err != nil || grant.ContextID != parent.ID || grant.SessionID != credential.Session.ID || grant.Permissions != comitia.PermissionRead|comitia.PermissionWrite || !grant.ExpiresAt.Equal(now.Add(time.Hour)) {
					t.Fatalf("grant: %+v %v", grant, err)
				}
				child, err := client.CreateChildContext(ctx, owner.Token, comitia.CreateChildContextInput{ParentContextID: parent.ID, TaskID: task.ID, Name: "child", ParentReadable: true})
				if err != nil || child.ParentContextID != parent.ID || child.WorkspaceID != workspace.ID || child.TaskID != task.ID || child.CreatorSessionID != owner.Session.ID || !child.ParentReadable {
					t.Fatalf("child metadata lost: %+v %v", child, err)
				}
				ref, err := client.AddReference(ctx, owner.Token, comitia.AddReferenceInput{ContextID: child.ID, Kind: "issue", URI: "https://example.test/55"})
				if err != nil || ref.ContextID != child.ID || ref.Kind != "issue" || ref.URI != "https://example.test/55" || !ref.CreatedAt.Equal(now) {
					t.Fatalf("reference: %+v %v", ref, err)
				}
				message, err := client.AppendMessage(ctx, owner.Token, parent.ID, comitia.MessageInput{DedupeKey: "mixed", Body: "hello", Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", Payload: []byte(`{}`)}})
				if err != nil || message.ContextID != parent.ID || message.SessionID != owner.Session.ID || message.DedupeKey != "mixed" || message.Sequence != 1 {
					t.Fatalf("message metadata lost: %+v %v", message, err)
				}
				// Filtered pages report progress past nonmatches separately from the
				// delivered cursor in every upgrade order.
				pager := client.(comitia.MessagePageAPI)
				for _, wait := range []bool{false, true} {
					var page comitia.MessagePage
					filter := comitia.MessageFilters{FromInstance: "nobody"}
					if wait {
						page, err = pager.WaitMessagesPage(ctx, owner.Token, parent.ID, 0, 0, filter)
					} else {
						page, err = pager.ReplayMessagesPage(ctx, owner.Token, parent.ID, 0, 100, filter)
					}
					if err != nil || len(page.Messages) != 0 || page.NextAfter != 0 || page.ScannedThrough != message.Sequence {
						t.Fatalf("filtered scan wait=%v: %+v %v", wait, page, err)
					}
				}
				_, helper, grants, err := client.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "helper", TTL: 2 * time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: parent.ID, Permissions: comitia.PermissionRead, ExpiresAt: now.Add(time.Hour)}, {ContextID: child.ID, Permissions: comitia.PermissionRead, ExpiresAt: now.Add(30 * time.Minute)}}})
				if err != nil || len(grants) != 2 || helper.Token == "" || helper.Session.ParentID != owner.Session.ID || !helper.Session.ExpiresAt.Equal(now.Add(2*time.Hour)) {
					t.Fatalf("delegate metadata lost: %+v %v", helper.Session, err)
				}
				for i, g := range grants {
					if g.SessionID != helper.Session.ID || g.Permissions != comitia.PermissionRead || g.ContextID != []string{parent.ID, child.ID}[i] || g.ParentGrantID == "" {
						t.Fatalf("delegated grant lost: %+v", g)
					}
				}
				if err := client.RevokeGrant(ctx, owner.Token, grant.ID); err != nil {
					t.Fatal(err)
				}
				if err := client.RevokeSession(ctx, owner.Token, helper.Session.ID); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestV1TaskUpgradeOrderProbe(t *testing.T) {
	for _, newServer := range []bool{false, true} {
		for _, newClient := range []bool{false, true} {
			name := map[bool]string{false: "old", true: "new"}[newClient] + "-client_" + map[bool]string{false: "old", true: "new"}[newServer] + "-handler"
			t.Run(name, func(t *testing.T) {
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
				workspace, err := svc.CreateWorkspace(ctx, owner.Token, "w")
				if err != nil {
					t.Fatal(err)
				}
				context, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: workspace.ID, Name: "c"})
				if err != nil {
					t.Fatal(err)
				}
				task, err := svc.CreateTask(ctx, owner.Token, context.ID, comitia.TaskInput{Title: "probe"})
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
				claim, err := client.ClaimTask(ctx, owner.Token, task.ID, time.Minute)
				if err != nil || claim.Task.LeaseVersion != 1 || claim.Task.LeaseOwnerSessionID == "" || claim.Task.LeaseExpiresAt == nil {
					t.Fatalf("claim: version=%d ownerPresent=%t expiryPresent=%t err=%v", claim.Task.LeaseVersion, claim.Task.LeaseOwnerSessionID != "", claim.Task.LeaseExpiresAt != nil, err)
				}
				renewed, err := client.RenewClaim(ctx, owner.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute)
				if err != nil || renewed.Task.LeaseVersion != 2 {
					t.Fatalf("renew: version=%d err=%v", renewed.Task.LeaseVersion, err)
				}
				if _, err := client.CompleteTask(ctx, owner.Token, task.ID, renewed.Token, renewed.Task.LeaseVersion); err != nil {
					t.Fatalf("complete: %v", err)
				}
			})
		}
	}
}
