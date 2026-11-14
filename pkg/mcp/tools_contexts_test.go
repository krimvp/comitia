package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/httpapi"
	"github.com/krimvp/comitia/pkg/sqlite"
)

func contextServer(t *testing.T, remote bool) (*Server, *comitia.Service, comitia.Credential, comitia.WorkContext) {
	t.Helper()
	store, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "once"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, admin, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "once", AdminName: "owner", SessionTTL: 24 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, admin.Token, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := svc.CreateContext(ctx, admin.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	_, coordinator, _, err := svc.DelegateSession(ctx, admin.Token, comitia.DelegateInput{PrincipalName: "coordinator", TTL: 8 * time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: parent.ID, Permissions: comitia.PermissionRead | comitia.PermissionWrite | comitia.PermissionClaim | comitia.PermissionGrant | comitia.PermissionCreate, ExpiresAt: time.Now().Add(7 * time.Hour)}}})
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{API: svc, Credential: coordinator.Token, SessionsDir: filepath.Join(t.TempDir(), "sessions"), ServerURL: "http://127.0.0.1:8390"}
	if remote {
		httpServer := httptest.NewServer(httpapi.NewHandler(svc))
		t.Cleanup(httpServer.Close)
		client, err := httpapi.NewClient(httpServer.URL, httpServer.Client())
		if err != nil {
			t.Fatal(err)
		}
		server.API, server.ServerURL = client, httpServer.URL
	}
	return server, svc, admin, parent
}

func callTool(t *testing.T, s *Server, name string, args any) any {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.call(context.Background(), name, raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func TestContextToolsAuthorizationMetadataAndPagination(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded", true: "remote"}[remote], func(t *testing.T) {
			s, svc, admin, parent := contextServer(t, remote)
			ctx := context.Background()
			task, err := svc.CreateTask(ctx, s.Credential, parent.ID, comitia.TaskInput{Title: "work"})
			if err != nil {
				t.Fatal(err)
			}
			first := callTool(t, s, "create_context", map[string]any{"parent_context_id": parent.ID, "name": "private", "task_id": task.ID}).(comitia.WorkContext)
			second := callTool(t, s, "create_context", map[string]any{"parent_context_id": parent.ID, "name": "shared", "parent_readable": true}).(comitia.WorkContext)
			if first.ParentContextID != parent.ID || first.TaskID != task.ID || first.CreatorSessionID == "" || first.ParentReadable || !second.ParentReadable {
				t.Fatalf("metadata: %+v %+v", first, second)
			}
			if _, err := svc.GetContext(ctx, admin.Token, first.ID); !errors.Is(err, comitia.ErrForbidden) {
				t.Fatalf("child privacy: %v", err)
			}
			if _, err := svc.GetContext(ctx, admin.Token, second.ID); err != nil {
				t.Fatal(err)
			}
			var contexts []comitia.WorkContext
			cursor := ""
			for {
				page := callTool(t, s, "list_contexts", map[string]any{"after_id": cursor, "limit": 1}).([]comitia.WorkContext)
				if len(page) == 0 {
					break
				}
				if len(page) != 1 || page[0].ID <= cursor {
					t.Fatalf("pagination: %+v", page)
				}
				contexts = append(contexts, page...)
				cursor = page[0].ID
			}
			if len(contexts) != 3 {
				t.Fatalf("context count=%d", len(contexts))
			}
			page := callTool(t, s, "list_child_contexts", map[string]any{"parent_context_id": parent.ID, "limit": 1}).([]comitia.WorkContext)
			rest := callTool(t, s, "list_child_contexts", map[string]any{"parent_context_id": parent.ID, "after_id": page[0].ID}).([]comitia.WorkContext)
			if len(rest) != 1 || rest[0].ID <= page[0].ID {
				t.Fatalf("children: %+v", rest)
			}
			for _, args := range []map[string]any{{"parent_context_id": parent.ID, "name": "bad", "task_id": "missing"}, {"name": "top level"}, {"parent_context_id": parent.ID, "name": "bad", "workspace_id": parent.WorkspaceID}} {
				raw, _ := json.Marshal(args)
				if _, err := s.call(ctx, "create_context", raw); !errors.Is(err, comitia.ErrInvalid) {
					t.Fatalf("invalid: %v", err)
				}
			}
			s.Credential = admin.Token
			children := callTool(t, s, "list_child_contexts", map[string]any{"parent_context_id": parent.ID}).([]comitia.WorkContext)
			if len(children) != 2 {
				t.Fatal("parent readers must see private child metadata")
			}
			_, reader, _, err := svc.DelegateSession(ctx, admin.Token, comitia.DelegateInput{PrincipalName: "reader", TTL: time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: parent.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(30 * time.Minute)}}})
			if err != nil {
				t.Fatal(err)
			}
			s.Credential = reader.Token
			raw, _ := json.Marshal(map[string]any{"parent_context_id": parent.ID, "name": "forbidden"})
			if _, err := s.call(ctx, "create_context", raw); !errors.Is(err, comitia.ErrForbidden) {
				t.Fatalf("create without permission: %v", err)
			}
		})
	}
}
