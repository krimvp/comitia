package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

func TestContextLifecycleToolsEmbeddedAndRemote(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded", true: "remote"}[remote], func(t *testing.T) {
			s, svc, admin, parent := contextServer(t, remote)
			ctx := context.Background()
			got := callTool(t, s, "get_context", map[string]any{"context_id": parent.ID}).(comitia.WorkContext)
			if got.ID != parent.ID || got.Name != parent.Name {
				t.Fatalf("get: %+v", got)
			}
			ref := callTool(t, s, "add_reference", map[string]any{"context_id": parent.ID, "kind": "issue", "uri": "https://example.test/55"}).(comitia.Reference)
			if ref.ID == "" || ref.ContextID != parent.ID || ref.Kind != "issue" || ref.URI != "https://example.test/55" || ref.CreatedAt.IsZero() {
				t.Fatalf("reference: %+v", ref)
			}
			refs := callTool(t, s, "list_references", map[string]any{"context_id": parent.ID}).(comitia.ReferencePage)
			if len(refs.References) != 1 || refs.References[0] != ref || refs.HasMore || refs.NextAfter == "" {
				t.Fatalf("list references: %+v", refs)
			}
			if text := resultText(resultCall(t, s, "list_references", map[string]any{"context_id": parent.ID})); text != "1 references; next_after="+refs.NextAfter+"; has_more=false. Full result in structuredContent." {
				t.Fatalf("summary: %s", text)
			}
			if _, err := s.call(ctx, "list_references", json.RawMessage(`{"context_id":"`+parent.ID+`","limit":0}`)); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("list limit: %v", err)
			}
			if _, err := s.call(ctx, "add_reference", json.RawMessage(`{"context_id":"`+parent.ID+`","kind":"issue","uri":"not a uri"}`)); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("unschemed uri: %v", err)
			}
			child := callTool(t, s, "create_context", map[string]any{"parent_context_id": parent.ID, "name": "archive"}).(comitia.WorkContext)
			_, reader, _, err := svc.DelegateSession(ctx, s.Credential, comitia.DelegateInput{PrincipalName: "reader", TTL: time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: child.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(30 * time.Minute)}}})
			if err != nil {
				t.Fatal(err)
			}
			readerServer := &Server{API: s.API, Credential: reader.Token}
			for _, name := range []string{"archive_context", "add_reference"} {
				raw := json.RawMessage(`{"context_id":"` + child.ID + `"}`)
				if name == "add_reference" {
					raw = json.RawMessage(`{"context_id":"` + child.ID + `","kind":"issue","uri":"https://example.test/denied"}`)
				}
				if _, err := readerServer.call(ctx, name, raw); !errors.Is(err, comitia.ErrForbidden) {
					t.Fatalf("reader %s: %v", name, err)
				}
			}
			if _, err := s.call(ctx, "get_context", json.RawMessage(`{"context_id":"`+child.ID+`","grant_id":"misplaced"}`)); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("strict get: %v", err)
			}
			if _, err := s.call(ctx, "add_reference", json.RawMessage(`{"context_id":"`+parent.ID+`","kind":"issue"}`)); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("invalid reference: %v", err)
			}
			if _, err := s.call(ctx, "archive_context", json.RawMessage(`{}`)); !errors.Is(err, comitia.ErrInvalid) {
				t.Fatalf("invalid archive: %v", err)
			}
			callTool(t, s, "archive_context", map[string]any{"context_id": child.ID})
			got = callTool(t, readerServer, "get_context", map[string]any{"context_id": child.ID}).(comitia.WorkContext)
			if !got.Archived || got.ArchivedAt == nil {
				t.Fatalf("archive result: %+v", got)
			}
			if page := callTool(t, readerServer, "list_references", map[string]any{"context_id": child.ID}).(comitia.ReferencePage); len(page.References) != 0 || page.HasMore {
				t.Fatalf("archived references: %+v", page)
			}
			// Global administration does not implicitly grant private child read.
			adminServer := &Server{API: s.API, Credential: admin.Token}
			if _, err := adminServer.call(ctx, "get_context", json.RawMessage(`{"context_id":"`+child.ID+`"}`)); !errors.Is(err, comitia.ErrForbidden) {
				t.Fatalf("private get: %v", err)
			}
		})
	}
	for _, name := range []string{"get_context", "add_reference", "list_references", "archive_context"} {
		found := false
		for _, tool := range contextTools() {
			if tool["name"] == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing tool schema: %s", name)
		}
	}
}
