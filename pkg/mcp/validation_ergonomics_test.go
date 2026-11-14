package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

func TestActionableMessageErrorsAcrossAdapters(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded", true: "HTTP"}[remote], func(t *testing.T) {
			s, svc, owner, c := contextServer(t, remote)
			ctx := context.Background()
			root, err := svc.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: "root", Body: "private-root"})
			if err != nil {
				t.Fatal(err)
			}
			for _, tc := range []struct {
				tool string
				args map[string]any
				want []string
			}{
				{"replay_messages", map[string]any{"context_id": c.ID, "after_sequence": 0}, []string{`unknown field "after_sequence"`, "accepted fields:", `did you mean "after"?`, "this client is comitia"}},
				{"ack_messages", map[string]any{"context_id": c.ID, "through": 0}, []string{`unknown field "through"`, "accepted fields:", `did you mean "sequence"?`}},
				{"send_message", map[string]any{"context_id": c.ID, "dedupe_key": "payload", "envelope": map[string]any{"schema": "comitia.finding.v1", "payload": map[string]any{"summry": "private-value", "severity": "info"}}}, []string{`unknown field "summry"`, "accepted fields:", `did you mean "summary"?`}},
				{"send_message", map[string]any{"context_id": c.ID, "dedupe_key": "nested", "envelope": map[string]any{"schma": "private-value", "payload": map[string]any{}}}, []string{`unknown field "schma"`, "accepted fields:", `did you mean "schema"?`}},
				{"send_message", map[string]any{"context_id": c.ID, "dedupe_key": "schema", "envelope": map[string]any{"schema": "private-value", "payload": map[string]any{}}}, []string{"schema must name a registered message schema", "list_message_schemas", "comitia.discussion.v1"}},
			} {
				info := operationalToolError(t, s, tc.tool, tc.args)
				if info.Code != "invalid" {
					t.Fatalf("%s: %+v", tc.tool, info)
				}
				for _, want := range tc.want {
					if !strings.Contains(info.Message, want) {
						t.Fatalf("%s missing %q: %+v", tc.tool, want, info)
					}
				}
				if strings.Contains(info.Message, "private-value") {
					t.Fatalf("value disclosed: %+v", info)
				}
			}
			for _, tool := range []string{"replay_messages", "wait_messages"} {
				args := map[string]any{"context_id": c.ID, "schema": "example.future.v1"}
				if tool == "wait_messages" {
					args["timeout_seconds"] = 0
				}
				raw, _ := json.Marshal(args)
				messages, err := s.call(ctx, tool, raw)
				if err != nil || len(messages.([]comitia.Message)) != 0 {
					t.Fatalf("unregistered %s filter: %v %v", tool, messages, err)
				}
			}
			for _, field := range []string{"reply_to", "task_id", "recipient_session_ids"} {
				env := map[string]any{"schema": "comitia.discussion.v1", "payload": map[string]any{}}
				target := any("missing-private-id")
				if field == "recipient_session_ids" {
					target = []string{"missing-private-id"}
				}
				env[field] = target
				info := operationalToolError(t, s, "send_message", map[string]any{"context_id": c.ID, "dedupe_key": field, "body": "note", "envelope": env})
				if info.Code != "forbidden" || !strings.Contains(info.Message, field) || strings.Contains(info.Message, "missing-private-id") {
					t.Fatalf("reference error: %+v", info)
				}
			}
			_, writer, _, err := svc.DelegateSession(ctx, owner.Token, comitia.DelegateInput{PrincipalName: "write-only", TTL: time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: c.ID, Permissions: comitia.PermissionWrite, ExpiresAt: time.Now().Add(30 * time.Minute)}}})
			if err != nil {
				t.Fatal(err)
			}
			s.Credential = writer.Token
			var previous string
			for _, target := range []string{root.ID, "missing-private-id"} {
				info := operationalToolError(t, s, "send_message", map[string]any{"context_id": c.ID, "dedupe_key": target, "body": "reply", "envelope": map[string]any{"schema": "comitia.discussion.v1", "reply_to": target, "payload": json.RawMessage(`{}`)}})
				if info.Code != "forbidden" || !strings.Contains(info.Message, "reply_to") || strings.Contains(info.Message, target) {
					t.Fatalf("writer reply: %+v", info)
				}
				if previous != "" && previous != info.Message {
					t.Fatalf("existence leak: %q != %q", previous, info.Message)
				}
				previous = info.Message
			}
		})
	}
}
