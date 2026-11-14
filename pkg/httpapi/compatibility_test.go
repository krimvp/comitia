package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/httpapi"
	"github.com/krimvp/comitia/pkg/httpapi/internal/legacy"
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
				if err != nil || message.ContextID != parent.ID || message.SessionID != owner.Session.ID || message.DedupeKey != "mixed" || message.Sequence != 8 {
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
				// Plain discovery lists work in every order. New Clients page against
				// old union Handlers, which ignore the paging members and omit the page.
				tasks, err := client.ListTasks(ctx, owner.Token, parent.ID, "", 100)
				if err != nil || len(tasks) != 2 {
					t.Fatalf("tasks: %+v %v", tasks, err)
				}
				participants, err := client.ListParticipants(ctx, owner.Token, parent.ID, 100)
				if err != nil || len(participants) != 2 {
					t.Fatalf("participants: %+v %v", participants, err)
				}
				if newClient {
					first, err := comitia.ListTaskPage(ctx, client, owner.Token, parent.ID, "", "", 1)
					if err != nil || len(first.Tasks) != 1 || !first.HasMore || first.Tasks[0].LeaseTokenHash != "" {
						t.Fatalf("task page: %+v %v", first, err)
					}
					rest, err := comitia.ListTaskPage(ctx, client, owner.Token, parent.ID, "", first.NextAfter, 1)
					if err != nil || len(rest.Tasks) != 1 || rest.HasMore || rest.Tasks[0].ID == first.Tasks[0].ID {
						t.Fatalf("task continuation: %+v %v", rest, err)
					}
					people, err := comitia.ListParticipantPage(ctx, client, owner.Token, parent.ID, "", 1)
					if err != nil || len(people.Participants) != 1 || !people.HasMore {
						t.Fatalf("participant page: %+v %v", people, err)
					}
					more, err := comitia.ListParticipantPage(ctx, client, owner.Token, parent.ID, people.NextAfter, 1)
					if err != nil || len(more.Participants) != 1 || more.HasMore || more.Participants[0].SessionID == people.Participants[0].SessionID {
						t.Fatalf("participant continuation: %+v %v", more, err)
					}
				}
				// Schema discovery and strict recipients need a new Handler. Against an
				// old one, a new Client fails clearly and never widens a strict read.
				if newClient {
					discovery := client.(comitia.MessageSchemaAPI)
					schemas, err := discovery.ListMessageSchemas(ctx, owner.Token)
					_, getErr := discovery.GetMessageSchema(ctx, owner.Token, "comitia.discussion.v1")
					strict, strictErr := client.ReplayMessages(ctx, owner.Token, parent.ID, 0, 100, comitia.MessageFilters{ForInstance: "reviewer", StrictRecipients: true})
					if newServer {
						if err != nil || len(schemas) == 0 || getErr != nil || strictErr != nil {
							t.Fatalf("schemas/strict: %d %v %v %v", len(schemas), err, getErr, strictErr)
						}
					} else {
						for _, err := range []error{err, getErr, strictErr} {
							if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "unsupported by this server") {
								t.Fatalf("old handler capability error: %v", err)
							}
						}
						if strict != nil {
							t.Fatalf("strict read widened: %v", strict)
						}
					}
				}
				// Old Clients omit an ack of 0; every pairing still acknowledges it.
				if err := client.AckMessages(ctx, owner.Token, parent.ID, 0); err != nil {
					t.Fatalf("ack zero: %v", err)
				}
				if err := client.AckMessages(ctx, owner.Token, parent.ID, message.Sequence); err != nil {
					t.Fatalf("ack: %v", err)
				}
				if cursor, err := client.MessageCursor(ctx, owner.Token, parent.ID); err != nil || cursor != message.Sequence {
					t.Fatalf("ack cursor: %d %v", cursor, err)
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

// Task receipts and result fences are new request members. A new Client must
// report an old server's strict rejection as unsupported, never resend the
// request without its operation ID or fence, and old Clients keep working.
func TestV1TaskRecoveryUpgradeOrder(t *testing.T) {
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
				room, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: workspace.ID, Name: "c"})
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
				countTasks := func() int {
					tasks, err := svc.ListTasks(ctx, owner.Token, room.ID, "", 100)
					if err != nil {
						t.Fatal(err)
					}
					return len(tasks)
				}
				result := func(key, taskID string, fence *comitia.TaskFence) comitia.MessageInput {
					return comitia.MessageInput{DedupeKey: key, Body: "done", TaskFence: fence, Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", TaskID: taskID, Payload: json.RawMessage(`{}`)}}
				}
				switch {
				case !newClient:
					// Old Clients have no recovery capability, and plain task calls and
					// reads of fenced results from either server keep working.
					if _, err := comitia.CreateTaskOperation(ctx, client, owner.Token, room.ID, comitia.TaskInput{Title: "x", OperationID: "op"}); !errors.Is(err, comitia.ErrInvalid) || countTasks() != 0 {
						t.Fatalf("old client operation: %v", err)
					}
					task, err := client.CreateTask(ctx, owner.Token, room.ID, comitia.TaskInput{Title: "plain"})
					if err != nil {
						t.Fatal(err)
					}
					claim, err := client.ClaimTask(ctx, owner.Token, task.ID, time.Minute)
					if err != nil {
						t.Fatal(err)
					}
					fenced, err := svc.AppendMessage(ctx, owner.Token, room.ID, result("fenced", task.ID, &comitia.TaskFence{TaskID: task.ID, ClaimToken: claim.Token, LeaseVersion: claim.Task.LeaseVersion}))
					if err != nil || fenced.TaskAttempt == nil {
						t.Fatalf("fenced append: %+v %v", fenced, err)
					}
					messages, err := client.ReplayMessages(ctx, owner.Token, room.ID, 0, 10, comitia.MessageFilters{Schema: "comitia.discussion.v1"})
					if err != nil || len(messages) != 1 || messages[0].ID != fenced.ID {
						t.Fatalf("old client reads fenced result: %+v %v", messages, err)
					}
				case !newServer:
					recovery := client.(comitia.TaskMutationAPI)
					if _, err := client.CreateTask(ctx, owner.Token, room.ID, comitia.TaskInput{Title: "x", OperationID: "create"}); !errors.Is(err, httpapi.ErrTaskMutationReceiptsUnsupported) || countTasks() != 0 {
						t.Fatalf("create receipt on old server: %v (tasks=%d)", err, countTasks())
					}
					task, err := client.CreateTask(ctx, owner.Token, room.ID, comitia.TaskInput{Title: "plain"})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := recovery.ClaimTaskWithOperation(ctx, owner.Token, task.ID, time.Minute, "claim"); !errors.Is(err, httpapi.ErrTaskMutationReceiptsUnsupported) {
						t.Fatalf("claim receipt on old server: %v", err)
					}
					if current, _ := svc.GetTask(ctx, owner.Token, task.ID); current.Status != comitia.TaskPending {
						t.Fatalf("unsupported claim was applied: %+v", current)
					}
					claim, err := client.ClaimTask(ctx, owner.Token, task.ID, time.Minute)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := recovery.RenewClaimWithOperation(ctx, owner.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute, "renew"); !errors.Is(err, httpapi.ErrTaskMutationReceiptsUnsupported) {
						t.Fatalf("renew receipt on old server: %v", err)
					}
					if current, _ := svc.GetTask(ctx, owner.Token, task.ID); current.LeaseVersion != claim.Task.LeaseVersion {
						t.Fatalf("unsupported renew was applied: %+v", current)
					}
					fence := &comitia.TaskFence{TaskID: task.ID, ClaimToken: claim.Token, LeaseVersion: claim.Task.LeaseVersion}
					if _, err := client.AppendMessage(ctx, owner.Token, room.ID, result("fenced", task.ID, fence)); !errors.Is(err, httpapi.ErrTaskResultFencingUnsupported) {
						t.Fatalf("fence on old server: %v", err)
					}
					if messages, _ := svc.ReplayMessages(ctx, owner.Token, room.ID, 0, 10, comitia.MessageFilters{Schema: "comitia.discussion.v1"}); len(messages) != 0 {
						t.Fatalf("unfenced copy appended: %+v", messages)
					}
				default:
					recovery := client.(comitia.TaskMutationAPI)
					in := comitia.TaskInput{Title: "job", OperationID: "create"}
					task, err := client.CreateTask(ctx, owner.Token, room.ID, in)
					if err != nil {
						t.Fatal(err)
					}
					if again, err := client.CreateTask(ctx, owner.Token, room.ID, in); err != nil || again.ID != task.ID || countTasks() != 1 {
						t.Fatalf("create retry: %+v %v", again, err)
					}
					first, err := recovery.ClaimTaskWithOperation(ctx, owner.Token, task.ID, time.Minute, "claim")
					if err != nil {
						t.Fatal(err)
					}
					claim, err := recovery.ClaimTaskWithOperation(ctx, owner.Token, task.ID, time.Minute, "claim")
					if err != nil || claim.Task.LeaseVersion != first.Task.LeaseVersion || claim.Token == first.Token || claim.Task.LeaseTokenHash != "" {
						t.Fatalf("claim retry: %+v %v", claim.Task, err)
					}
					renewed, err := recovery.RenewClaimWithOperation(ctx, owner.Token, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute, "renew")
					if err != nil || renewed.Task.LeaseVersion != claim.Task.LeaseVersion+1 {
						t.Fatalf("renew: %+v %v", renewed.Task, err)
					}
					fence := &comitia.TaskFence{TaskID: task.ID, ClaimToken: renewed.Token, LeaseVersion: renewed.Task.LeaseVersion}
					accepted, err := client.AppendMessage(ctx, owner.Token, room.ID, result("fenced", task.ID, fence))
					if err != nil || accepted.TaskAttempt == nil || accepted.TaskAttempt.TaskID != task.ID || accepted.TaskAttempt.LeaseVersion != renewed.Task.LeaseVersion || accepted.TaskAttempt.ClaimTokenHash != "" {
						t.Fatalf("fenced append: %+v %v", accepted.TaskAttempt, err)
					}
					stale := &comitia.TaskFence{TaskID: task.ID, ClaimToken: first.Token, LeaseVersion: first.Task.LeaseVersion}
					if _, err := client.AppendMessage(ctx, owner.Token, room.ID, result("stale", task.ID, stale)); !errors.Is(err, comitia.ErrStaleClaim) {
						t.Fatalf("stale fence: %v", err)
					}
					// Both representations publish the attempt, never its token hash.
					for _, canonical := range []bool{false, true} {
						req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/messages/replay", strings.NewReader(`{"context_id":"`+room.ID+`"}`))
						req.Header.Set("Authorization", "Bearer "+owner.Token)
						req.Header.Set("Content-Type", "application/json")
						if canonical {
							req.Header.Set("X-Comitia-Wire", "canonical")
						}
						resp, err := server.Client().Do(req)
						if err != nil {
							t.Fatal(err)
						}
						body, _ := io.ReadAll(resp.Body)
						resp.Body.Close()
						want := fmt.Sprintf(`"task_attempt":{"task_id":%q,"lease_version":%d}`, task.ID, renewed.Task.LeaseVersion)
						if !strings.Contains(string(body), want) || strings.Contains(strings.ToLower(string(body)), "token") {
							t.Fatalf("canonical=%v replay: %s", canonical, body)
						}
					}
				}
			})
		}
	}
}

// Endpoint-DTO Handlers before issue 62 reject the members while decoding.
// The Client must surface that once instead of retrying without them.
func TestClientReportsUnsupportedTaskRecoveryFromDTOHandler(t *testing.T) {
	requests := 0
	field := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(comitia.ErrorInfo{Code: "invalid", Message: fmt.Sprintf("invalid: unknown field %q", field)})
	}))
	defer server.Close()
	client, err := httpapi.NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, call := range map[string]func() error{
		"create": func() error {
			_, err := client.CreateTask(ctx, "tok", "c", comitia.TaskInput{Title: "x", OperationID: "op"})
			return err
		},
		"claim": func() error {
			_, err := client.ClaimTaskWithOperation(ctx, "tok", "t", time.Minute, "op")
			return err
		},
		"renew": func() error {
			_, err := client.RenewClaimWithOperation(ctx, "tok", "t", "k", 1, time.Minute, "op")
			return err
		},
	} {
		requests, field = 0, "operation_id"
		if err := call(); !errors.Is(err, httpapi.ErrTaskMutationReceiptsUnsupported) || requests != 1 {
			t.Fatalf("%s: requests=%d err=%v", name, requests, err)
		}
	}
	requests, field = 0, "task_fence"
	_, err = client.AppendMessage(ctx, "tok", "c", comitia.MessageInput{DedupeKey: "k", Body: "b", TaskFence: &comitia.TaskFence{TaskID: "t", ClaimToken: "k", LeaseVersion: 1}})
	if !errors.Is(err, httpapi.ErrTaskResultFencingUnsupported) || requests != 1 {
		t.Fatalf("fence: requests=%d err=%v", requests, err)
	}
	// Calls without the new members keep the server's own error.
	requests, field = 0, "operation_id"
	if _, err := client.CreateTask(ctx, "tok", "c", comitia.TaskInput{Title: "x"}); errors.Is(err, httpapi.ErrTaskMutationReceiptsUnsupported) || !errors.Is(err, comitia.ErrInvalid) {
		t.Fatalf("plain create: %v", err)
	}
}

func TestV1TaskOutcomeUpgradeOrder(t *testing.T) {
	for _, newServer := range []bool{false, true} {
		for _, newClient := range []bool{false, true} {
			t.Run(fmt.Sprintf("server=%v/client=%v", newServer, newClient), func(t *testing.T) {
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
				room, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: workspace.ID, Name: "c"})
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
				for _, status := range []comitia.TaskStatus{comitia.TaskFailed, comitia.TaskCancelled} {
					task, err := client.CreateTask(ctx, owner.Token, room.ID, comitia.TaskInput{Title: "job"})
					if err != nil {
						t.Fatal(err)
					}
					c, err := client.ClaimTask(ctx, owner.Token, task.ID, time.Minute)
					if err != nil {
						t.Fatal(err)
					}
					in := comitia.TaskOutcomeInput{ClaimToken: c.Token, LeaseVersion: c.Task.LeaseVersion, Reason: "stopped", OperationID: string(status)}
					call := comitia.FailTask
					if status == comitia.TaskCancelled {
						call = comitia.CancelTask
					}
					result, err := call(ctx, client, owner.Token, task.ID, in)
					if !newClient || !newServer {
						if !errors.Is(err, comitia.ErrInvalid) {
							t.Fatalf("unsupported outcome: %v", err)
						}
						if newClient && !errors.Is(err, httpapi.ErrTaskOutcomesUnsupported) {
							t.Fatalf("old server: %v", err)
						}
						current, e := svc.GetTask(ctx, owner.Token, task.ID)
						if e != nil || current.Status != comitia.TaskLeased {
							t.Fatalf("unsupported outcome applied: %+v %v", current, e)
						}
						if status == comitia.TaskFailed {
							result, err = svc.FailTask(ctx, owner.Token, task.ID, in)
						} else {
							result, err = svc.CancelTask(ctx, owner.Token, task.ID, in)
						}
					}
					if err != nil || result.Status != status || result.OutcomeReason != in.Reason {
						t.Fatalf("outcome: %+v %v", result, err)
					}
					current, err := client.GetTask(ctx, owner.Token, task.ID)
					if err != nil || current.Status != status || current.OutcomeReason != in.Reason {
						t.Fatalf("outcome read: %+v %v", current, err)
					}
					if newClient && newServer {
						again, err := call(ctx, client, owner.Token, task.ID, in)
						if err != nil || again.Status != status {
							t.Fatalf("outcome receipt: %+v %v", again, err)
						}
						listed, err := client.ListTasks(ctx, owner.Token, room.ID, status, 10)
						if err != nil || len(listed) != 1 || listed[0].OutcomeReason != in.Reason {
							t.Fatalf("outcome list: %+v %v", listed, err)
						}
					}
					messages, err := client.ReplayMessages(ctx, owner.Token, room.ID, 0, 100, comitia.MessageFilters{TaskID: task.ID})
					if err != nil || len(messages) != 3 || messages[2].Envelope.Schema != comitia.TaskEventSchema {
						t.Fatalf("events: %+v %v", messages, err)
					}
				}
			})
		}
	}
}

func TestClientReportsUnsupportedTaskOutcomeFields(t *testing.T) {
	for _, field := range []string{"reason", "operation_id"} {
		requests := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["reason"] != "stopped" || body["operation_id"] != "finish" {
				t.Errorf("semantics missing: %+v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(comitia.ErrorInfo{Code: "invalid", Message: fmt.Sprintf("invalid: unknown field %q", field)})
		}))
		client, err := httpapi.NewClient(server.URL, server.Client())
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range []func(context.Context, string, string, comitia.TaskOutcomeInput) (comitia.Task, error){client.FailTask, client.CancelTask} {
			requests = 0
			_, err := call(context.Background(), "tok", "t", comitia.TaskOutcomeInput{ClaimToken: "claim", LeaseVersion: 1, Reason: "stopped", OperationID: "finish"})
			if !errors.Is(err, httpapi.ErrTaskOutcomesUnsupported) || requests != 1 {
				t.Fatalf("field %s: requests=%d err=%v", field, requests, err)
			}
		}
		server.Close()
	}
}

// Optional lookup/scoped queries never add methods to API. Old clients retain
// plain reads against new handlers; new clients refuse old union handlers that
// would otherwise silently ignore a scoped query member.
func TestV1ScopedMessageUpgradeOrder(t *testing.T) {
	for _, newServer := range []bool{false, true} {
		for _, newClient := range []bool{false, true} {
			t.Run(fmt.Sprintf("new-client=%v/new-server=%v", newClient, newServer), func(t *testing.T) {
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
				root, err := svc.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "root", Body: "root"})
				if err != nil {
					t.Fatal(err)
				}
				reply, err := svc.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "reply", Body: "full\nreply", Envelope: &comitia.MessageEnvelope{Schema: "comitia.discussion.v1", ReplyTo: root.ID, Payload: json.RawMessage(`{}`)}})
				if err != nil {
					t.Fatal(err)
				}
				h := legacy.NewHandler(svc)
				if newServer {
					h = httpapi.NewHandler(svc)
				}
				server := httptest.NewServer(h)
				defer server.Close()
				var api comitia.API
				if newClient {
					api, err = httpapi.NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
				} else {
					api, err = legacy.NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
				}
				if err != nil {
					t.Fatal(err)
				}
				plain, err := api.ReplayMessages(ctx, owner.Token, c.ID, 0, 100)
				if err != nil || len(plain) != 2 || plain[1].ID != reply.ID {
					t.Fatalf("plain upgrade: %v %v", plain, err)
				}
				if !newClient {
					if _, err := comitia.LookupMessage(ctx, api, owner.Token, c.ID, root.ID); !errors.Is(err, comitia.ErrInvalid) {
						t.Fatalf("old optional API: %v", err)
					}
					return
				}
				lookup, err := comitia.LookupMessage(ctx, api, owner.Token, c.ID, reply.ID)
				if newServer {
					if err != nil || lookup.SessionID != owner.Session.ID || lookup.Body != reply.Body || lookup.Envelope.ReplyTo != root.ID {
						t.Fatalf("lookup metadata: %+v %v", lookup, err)
					}
				} else if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "unsupported by this server") {
					t.Fatalf("old lookup: %v", err)
				}
				filters := []comitia.MessageFilters{{ReplyTo: root.ID}, {ThreadID: root.ID}, {FromSession: owner.Session.ID}, {ReplyTo: root.ID, ThreadID: root.ID, FromSession: owner.Session.ID}}
				for _, filter := range filters {
					for _, read := range []string{"replay", "wait", "replay-page", "wait-page"} {
						var messages []comitia.Message
						switch read {
						case "replay":
							messages, err = api.ReplayMessages(ctx, owner.Token, c.ID, 0, 100, filter)
						case "wait":
							messages, err = api.WaitMessages(ctx, owner.Token, c.ID, 0, 0, filter)
						case "replay-page":
							var page comitia.MessagePage
							page, err = comitia.ReplayMessagePage(ctx, api, owner.Token, c.ID, 0, 100, filter)
							messages = page.Messages
						case "wait-page":
							var page comitia.MessagePage
							page, err = comitia.WaitMessagePage(ctx, api, owner.Token, c.ID, 0, 0, filter)
							messages = page.Messages
						}
						if !newServer {
							if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "unsupported by this server") || messages != nil {
								t.Fatalf("widened old %s %+v: %v %v", read, filter, messages, err)
							}
						} else {
							want := 2
							if filter.ReplyTo != "" {
								want = 1
							}
							if err != nil || len(messages) != want || messages[len(messages)-1].ID != reply.ID {
								t.Fatalf("new %s %+v: %v %v", read, filter, messages, err)
							}
						}
					}
				}
			})
		}
	}
}

// A DTO predecessor rejects new filters. Do not retry without them even if a
// proxy/embedding advertises query support too optimistically.
func TestClientScopedQueryDTORejection(t *testing.T) {
	requests := 0
	field := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/messages/query-support" {
			_, _ = io.WriteString(w, `{"ok":true}`)
			return
		}
		requests++
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(comitia.ErrorInfo{Code: "invalid", Message: fmt.Sprintf("invalid: unknown field %q", field)})
	}))
	defer server.Close()
	client, err := httpapi.NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		filter comitia.MessageFilters
	}{{"reply_to", comitia.MessageFilters{ReplyTo: "message"}}, {"thread_id", comitia.MessageFilters{ThreadID: "message"}}, {"from_session", comitia.MessageFilters{FromSession: "session"}}} {
		field, requests = tc.name, 0
		messages, err := client.ReplayMessages(context.Background(), "token", "context", 0, 100, tc.filter)
		if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "unsupported by this server") || requests != 1 || messages != nil {
			t.Fatalf("field %s retry/widening: %d %v %v", field, requests, messages, err)
		}
	}
}

func TestClientProtocolSchemasUnsupported(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(comitia.ErrorInfo{Code: "invalid", Message: "schema must name a registered message schema"})
	}))
	defer server.Close()
	client, err := httpapi.NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	for _, schema := range []string{comitia.RequestSchema, comitia.ReceiptSchema, comitia.MergeCertificateSchema, comitia.MergeReceiptSchema, comitia.StanceSchema} {
		requests = 0
		_, err := client.AppendMessage(context.Background(), "token", "context", comitia.MessageInput{DedupeKey: "stable", Envelope: &comitia.MessageEnvelope{Schema: schema, Payload: json.RawMessage(`{}`)}})
		if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), schema+" unsupported by this server") || requests != 1 {
			t.Fatalf("schema downgrade %s: %v requests=%d", schema, err, requests)
		}
	}
}
