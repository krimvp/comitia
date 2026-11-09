package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Accepted handler inputs must pass the schemas published to strict clients.
// Semantic checks (authority, live claims, nonempty reasons) may be stricter.
func TestAdvertisedSchemasAcceptedArguments(t *testing.T) {
	responses := serve(t, &fakeAPI{}, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`+"\n"+`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")
	listed := responses[1]["result"].(map[string]any)["tools"].([]any)
	fixture := schemaConformanceTemplate(t)
	for _, entry := range listed {
		definition := entry.(map[string]any)
		name := definition["name"].(string)
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var schemas []*jsonschema.Schema
			for _, draft := range []*jsonschema.Draft{jsonschema.Draft7, jsonschema.Draft2020} {
				compiler := jsonschema.NewCompiler()
				compiler.DefaultDraft(draft)
				url := "https://comitia.test/" + name
				if err := compiler.AddResource(url, definition["inputSchema"]); err != nil {
					t.Fatal(err)
				}
				compiled, err := compiler.Compile(url)
				if err != nil {
					t.Fatal(err)
				}
				schemas = append(schemas, compiled)
			}
			check := func(t *testing.T, path []string, mode string) {
				t.Helper()
				s, args := fixture(t, name)
				s.API = immediateWaitAPI{s.API.(*comitia.Service)}
				var raw []byte
				if len(path) == 0 && mode != "baseline" {
					args = nil
					switch mode {
					case "absent", "empty":
						args = map[string]any{}
					case "null":
						raw = []byte("null")
					}
				} else if len(path) != 0 {
					parent := args
					for _, key := range path[:len(path)-1] {
						if array, ok := parent[key].([]any); ok {
							parent = array[0].(map[string]any)
						} else {
							parent = parent[key].(map[string]any)
						}
					}
					key := path[len(path)-1]
					if _, ok := parent[key]; !ok {
						t.Fatalf("fixture lacks %s", strings.Join(path, "/"))
					}
					switch mode {
					case "absent":
						delete(parent, key)
					case "null":
						parent[key] = nil
					case "empty":
						switch parent[key].(type) {
						case string:
							parent[key] = ""
						case bool:
							parent[key] = false
						case float64:
							parent[key] = float64(0)
						case []any:
							parent[key] = []any{}
						case map[string]any:
							parent[key] = map[string]any{}
						default:
							t.Fatalf("no empty value for %s", key)
						}
					}
				}
				if raw == nil && !(len(path) == 0 && mode == "absent") {
					var err error
					raw, err = json.Marshal(args)
					if err != nil {
						t.Fatal(err)
					}
				}
				params, _ := json.Marshal(map[string]any{"name": name, "arguments": json.RawMessage(raw)})
				if len(raw) == 0 {
					params, _ = json.Marshal(map[string]any{"name": name})
				}
				res := s.handle(context.Background(), request{JSONRPC: "2.0", Method: "tools/call", Params: params})
				failed := res.Error != nil || res.Result.(map[string]any)["isError"] == true
				if mode == "baseline" && failed {
					t.Fatalf("invalid baseline: %v", res)
				}
				if failed {
					return
				}
				for i, compiled := range schemas {
					if err := compiled.Validate(args); err != nil {
						t.Errorf("draft %d rejects accepted input: %v", i, err)
					}
				}
			}
			t.Run("baseline", func(t *testing.T) { check(t, nil, "baseline") })
			for _, mode := range []string{"absent", "null", "empty"} {
				t.Run("arguments/"+mode, func(t *testing.T) { check(t, nil, mode) })
			}
			for _, path := range schemaPropertyPaths(definition["inputSchema"].(map[string]any), nil) {
				for _, mode := range []string{"absent", "null", "empty"} {
					t.Run(strings.Join(path, "/")+"/"+mode, func(t *testing.T) { check(t, path, mode) })
				}
			}
		})
	}
}

func schemaPropertyPaths(schema map[string]any, prefix []string) [][]string {
	var paths [][]string
	if alternatives, ok := schema["anyOf"].([]any); ok {
		for _, alternative := range alternatives {
			paths = append(paths, schemaPropertyPaths(alternative.(map[string]any), prefix)...)
		}
	}
	props, _ := schema["properties"].(map[string]any)
	var keys []string
	for key := range props {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		path := append(append([]string{}, prefix...), key)
		paths = append(paths, path)
		property := props[key].(map[string]any)
		if items, ok := property["items"].(map[string]any); ok {
			property = items
		}
		paths = append(paths, schemaPropertyPaths(property, path)...)
	}
	return paths
}

// immediateWaitAPI reads once instead of blocking: a wait whose arguments omit
// timeout_seconds defaults to 30s, and no fixture message matches every filter.
type immediateWaitAPI struct{ *comitia.Service }

func (a immediateWaitAPI) WaitMessages(ctx context.Context, token, cid string, after int64, _ time.Duration, filters ...comitia.MessageFilters) ([]comitia.Message, error) {
	return a.Service.WaitMessages(ctx, token, cid, after, 0, filters...)
}

func (a immediateWaitAPI) WaitMessagesPage(ctx context.Context, token, cid string, after int64, _ time.Duration, filters ...comitia.MessageFilters) (comitia.MessagePage, error) {
	return a.Service.WaitMessagesPage(ctx, token, cid, after, 0, filters...)
}

func schemaConformanceFixture(t *testing.T, name string) (*Server, map[string]any) {
	t.Helper()
	store, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	token, inputs := seedSchemaConformance(t, store)
	return schemaConformanceServer(t, store, token, inputs, name)
}

// schemaConformanceTemplate seeds and migrates one database file; each
// fixture it returns serves a private copy with the same IDs and credential.
func schemaConformanceTemplate(t *testing.T) func(*testing.T, string) (*Server, map[string]any) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "template.db")
	store, err := sqlite.OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	token, inputs := seedSchemaConformance(t, store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return func(t *testing.T, name string) (*Server, map[string]any) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "state.db")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		store, err := sqlite.OpenSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		return schemaConformanceServer(t, store, token, inputs, name)
	}
}

func seedSchemaConformance(t *testing.T, store comitia.Store) (string, map[string]map[string]any) {
	t.Helper()
	svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "schema-test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "schema-test", AdminName: "owner", SessionTTL: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := svc.CreateWorkspace(ctx, owner.Token, "schemas")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: workspace.ID, Name: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	_, helper, grants, err := svc.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "helper", TTL: time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: parent.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(30 * time.Minute)}}})
	if err != nil {
		t.Fatal(err)
	}
	pending, err := svc.CreateTask(ctx, owner.Token, parent.ID, comitia.TaskInput{Title: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := svc.CreateTask(ctx, owner.Token, parent.ID, comitia.TaskInput{Title: "leased"})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := svc.ClaimTask(ctx, owner.Token, task.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	root, err := svc.AppendMessage(ctx, owner.Token, parent.ID, comitia.MessageInput{DedupeKey: "schema-root", Body: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance := map[string]any{"id": "schema-worker", "label": "worker"}
	envelope := map[string]any{"schema": "comitia.discussion.v1", "payload": map[string]any{}, "task_id": task.ID, "reply_to": "", "recipient_session_ids": []any{helper.Session.ID}, "recipient_instances": []any{"schema-worker"}}
	inputs := map[string]map[string]any{
		"server_info": {}, "storage_status": {}, "list_message_schemas": {}, "get_message": {"context_id": parent.ID, "message_id": root.ID}, "get_message_schema": {"schema": comitia.TaskEventSchema},
		"send_message":      {"context_id": parent.ID, "dedupe_key": "schema-message", "recipient_session_ids": []any{}, "recipient_instances": []any{}, "body": "message", "envelope": envelope, "agent_instance": instance, "task_fence": map[string]any{"task_id": task.ID, "claim_token": claim.Token, "lease_version": claim.Task.LeaseVersion}},
		"list_participants": {"context_id": parent.ID, "limit": 10, "page": false, "after_id": ""},
		"replay_messages":   {"context_id": parent.ID, "after": 0, "limit": 10, "for_instance": "schema-worker", "from_instance": "", "task_id": task.ID, "schema": comitia.TaskEventSchema, "agent_instance": "schema-worker", "strict_recipients": true, "reply_to": root.ID, "thread_id": root.ID, "from_session": owner.Session.ID, "max_bytes": 4096, "page": false, "compact": false, "exclude_self": false, "from_cursor": false, "schemas": []any{}},
		"wait_messages":     {"context_id": parent.ID, "after": 0, "timeout_seconds": 0, "for_instance": "schema-worker", "from_instance": "", "task_id": task.ID, "schema": comitia.TaskEventSchema, "agent_instance": "schema-worker", "strict_recipients": true, "reply_to": root.ID, "thread_id": root.ID, "from_session": owner.Session.ID, "max_bytes": 4096, "page": false, "compact": false, "exclude_self": false, "from_cursor": false, "schemas": []any{}},
		"ack_messages":      {"context_id": parent.ID, "sequence": 0, "agent_instance": "schema-worker"},
		"message_cursor":    {"context_id": parent.ID, "agent_instance": "schema-worker"},
		"create_task":       {"context_id": parent.ID, "title": "new", "body": "details", "operation_id": "create"},
		"claim_task":        {"task_id": pending.ID, "operation_id": "claim", "lease_seconds": 60, "open_ended": false, "agent_instance": instance},
		"recover_claim":     {"task_id": task.ID, "lease_version": claim.Task.LeaseVersion, "agent_instance": instance},
		"renew_task":        {"task_id": task.ID, "claim_token": claim.Token, "lease_version": claim.Task.LeaseVersion, "operation_id": "renew", "lease_seconds": 60, "open_ended": false},
		"complete_task":     {"task_id": task.ID, "claim_token": claim.Token, "lease_version": claim.Task.LeaseVersion},
		"release_task":      {"task_id": task.ID, "claim_token": claim.Token, "lease_version": claim.Task.LeaseVersion},
		"fail_task":         {"task_id": task.ID, "claim_token": claim.Token, "lease_version": claim.Task.LeaseVersion, "reason": "failed", "operation_id": "fail"},
		"cancel_task":       {"task_id": pending.ID, "claim_token": "", "lease_version": 0, "reason": "unneeded", "operation_id": "cancel"},
		"get_task":          {"task_id": task.ID}, "list_tasks": {"claimable": false, "mine": false, "owner_session": "", "owner_instance": "", "context_id": parent.ID, "status": "leased", "limit": 10, "page": false, "after_id": ""},
		"get_context": {"context_id": parent.ID}, "archive_context": {"context_id": parent.ID},
		"add_reference":  {"context_id": parent.ID, "kind": "issue", "uri": "https://example.test/63"},
		"create_context": {"parent_context_id": parent.ID, "name": "child", "task_id": task.ID, "parent_readable": false},
		"list_contexts":  {"after_id": "", "limit": 10}, "list_child_contexts": {"parent_context_id": parent.ID, "after_id": "", "limit": 10},
		"grant_context": {"session_id": helper.Session.ID, "context_id": parent.ID, "permissions": []any{"read"}, "ttl_seconds": 60},
		"revoke_grant":  {"grant_id": grants[0].ID}, "revoke_session": {"session_id": helper.Session.ID},
		"delegate_session": {"name": "delegate", "ttl_seconds": 60, "grants": []any{map[string]any{"context_id": parent.ID, "permissions": []any{"read"}, "ttl_seconds": 30}}},
		"list_instances":   {"context_id": parent.ID}, "set_instance_resume": {"context_id": parent.ID, "agent_instance": instance, "resume": map[string]any{}},
	}
	return owner.Token, inputs
}

func schemaConformanceServer(t *testing.T, store comitia.Store, token string, inputs map[string]map[string]any, name string) (*Server, map[string]any) {
	t.Helper()
	svc, err := comitia.New(comitia.Config{Store: store})
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := comitia.NewInstanceDefaults(&comitia.AgentInstance{ID: "schema-worker"})
	if err != nil {
		t.Fatal(err)
	}
	sessions := t.TempDir()
	if err := os.Chmod(sessions, 0700); err != nil {
		t.Fatal(err)
	}
	s := &Server{API: svc, Credential: token, ServerURL: "http://127.0.0.1:1", SessionsDir: sessions, InstanceDefaults: defaults}
	if _, ok := inputs[name]; !ok {
		t.Fatalf("no conformance fixture for tool %s", name)
	}
	// Each fixture gets its own deep copy of the shared arguments.
	raw, err := json.Marshal(inputs[name])
	if err != nil {
		t.Fatal(err)
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		t.Fatal(err)
	}
	args["compact"] = false
	return s, args
}
