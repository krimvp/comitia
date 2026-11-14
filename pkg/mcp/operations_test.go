package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

func TestOperationalErrorMetadata(t *testing.T) {
	for _, err := range []error{comitia.ErrConflict, comitia.ErrStaleClaim, errors.New("private-token private-path private-body")} {
		s := &Server{API: &fakeAPI{err: err}, Credential: "credential"}
		res := s.handle(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage(`"private-client-id"`), Method: "tools/call", Params: json.RawMessage(`{"name":"get_task","arguments":{"task_id":"private-task"}}`)})
		b, _ := json.Marshal(res)
		var wire struct {
			Result struct {
				Structured struct {
					Error struct {
						Code, Hint string
						RequestID  string `json:"request_id"`
					} `json:"error"`
				} `json:"structuredContent"`
			} `json:"result"`
		}
		if json.Unmarshal(b, &wire) != nil || wire.Result.Structured.Error.Code == "" || wire.Result.Structured.Error.Hint == "" || wire.Result.Structured.Error.RequestID == "" {
			t.Fatalf("missing safe recovery metadata: %s", b)
		}
		for _, secret := range []string{"private-token", "private-path", "private-body", "private-task"} {
			if strings.Contains(string(b), secret) {
				t.Fatalf("leaked %q: %s", secret, b)
			}
		}
	}
}

func TestInitializeBuildProvenance(t *testing.T) {
	s := &Server{}
	for _, method := range []string{"initialize", "server/discover"} {
		res := s.handle(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: json.RawMessage(`{"protocolVersion":"2025-06-18"}`)})
		b, _ := json.Marshal(res)
		var wire struct {
			Result struct {
				Info struct {
					Build struct {
						Revision string `json:"revision"`
					} `json:"build"`
				} `json:"serverInfo"`
			} `json:"result"`
		}
		if json.Unmarshal(b, &wire) != nil || wire.Result.Info.Build.Revision == "" {
			t.Fatalf("missing provenance: %s", b)
		}
	}
}

func operationalToolError(t *testing.T, s *Server, name string, args any) comitia.ErrorInfo {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": args})
	if err != nil {
		t.Fatal(err)
	}
	res := s.handle(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "tools/call", Params: params})
	b, err := json.Marshal(res.Result)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		IsError    bool `json:"isError"`
		Structured struct {
			Error comitia.ErrorInfo `json:"error"`
		} `json:"structuredContent"`
	}
	if json.Unmarshal(b, &result) != nil || !result.IsError || result.Structured.Error.RequestID == "" {
		t.Fatalf("error result: %s", b)
	}
	return result.Structured.Error
}

func TestRecoveryHintsAfterAuthorization(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded", true: "remote"}[remote], func(t *testing.T) {
			s, svc, _, c := contextServer(t, remote)
			ctx := context.Background()
			original, err := s.API.AppendMessage(ctx, s.Credential, c.ID, comitia.MessageInput{DedupeKey: "private-key", Body: "private-body"})
			if err != nil {
				t.Fatal(err)
			}
			conflict := operationalToolError(t, s, "send_message", map[string]any{"context_id": c.ID, "dedupe_key": "private-key", "body": "private-changed-body"})
			if conflict.Code != "conflict" || !strings.Contains(conflict.Hint, "original message") {
				t.Fatalf("dedupe=%+v", conflict)
			}
			if d := conflict.Details; d == nil || d.MessageID != original.ID || d.Sequence != original.Sequence || strings.Join(d.Differs, ",") != "body" || !strings.Contains(conflict.Message, original.ID) {
				t.Fatalf("dedupe details=%+v", conflict)
			}
			task, err := svc.CreateTask(ctx, s.Credential, c.ID, comitia.TaskInput{Title: "private-title"})
			if err != nil {
				t.Fatal(err)
			}
			claim, err := s.API.ClaimTask(ctx, s.Credential, task.ID, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			held := operationalToolError(t, s, "claim_task", map[string]any{"task_id": task.ID})
			if held.Code != "conflict" || !strings.Contains(held.Hint, "get_task") {
				t.Fatalf("held=%+v", held)
			}
			if _, err := s.API.RenewClaim(ctx, s.Credential, task.ID, claim.Token, claim.Task.LeaseVersion, time.Minute); err != nil {
				t.Fatal(err)
			}
			stale := operationalToolError(t, s, "complete_task", map[string]any{"task_id": task.ID, "claim_token": claim.Token, "lease_version": claim.Task.LeaseVersion})
			if stale.Code != "stale_claim" || !strings.Contains(stale.Hint, "Token and Task.LeaseVersion") {
				t.Fatalf("stale=%+v", stale)
			}
			s.Credential = "invalid-secret"
			denied := operationalToolError(t, s, "claim_task", map[string]any{"task_id": task.ID})
			if denied.Code != "unauthenticated" || denied.Hint != "" {
				t.Fatalf("unauthorized state hint=%+v", denied)
			}
			for _, info := range []comitia.ErrorInfo{conflict, held, stale, denied} {
				if strings.Contains(info.String(), "private-") || strings.Contains(info.String(), claim.Token) || strings.Contains(info.String(), task.ID) {
					t.Fatalf("private detail leaked: %+v", info)
				}
			}
		})
	}
}

func TestStorageToolAndBuildIdentity(t *testing.T) {
	for _, remote := range []bool{false, true} {
		s, _, admin, _ := contextServer(t, remote)
		if _, err := s.call(context.Background(), "storage_status", json.RawMessage(`{}`)); !errors.Is(err, comitia.ErrForbidden) {
			t.Fatalf("non-admin=%v", err)
		}
		s.Credential = admin.Token
		result := callTool(t, s, "storage_status", map[string]any{}).(comitia.StorageStatus)
		if !result.WriteReady || result.WriteAttempts == 0 {
			t.Fatalf("storage=%+v", result)
		}
		info := callTool(t, s, "server_info", map[string]any{}).(comitia.ServerInfo)
		if info.Build != comitia.BuildIdentity() {
			t.Fatalf("build=%+v", info.Build)
		}
	}
}

func TestMCPFailureHookCorrelation(t *testing.T) {
	var diagnostic comitia.Diagnostic
	s := &Server{API: &fakeAPI{err: errors.New("private-error")}, Credential: "private-credential", DiagnosticHook: func(d comitia.Diagnostic) { diagnostic = d }}
	info := operationalToolError(t, s, "get_task", map[string]any{"task_id": "private-task"})
	if diagnostic.RequestID != info.RequestID || diagnostic.Operation != "get_task" || diagnostic.ErrorClass != "internal" {
		t.Fatalf("info=%+v diagnostic=%+v", info, diagnostic)
	}
}
