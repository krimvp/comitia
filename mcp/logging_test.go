package mcp

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLoggingPathCredentials(t *testing.T) {
	s, _, _, _ := contextServer(t, true)
	for _, tc := range []struct {
		name, token string
		status      int
	}{
		{"authenticated", s.Credential, http.StatusOK},
		{"rejected", "invalid-private-token", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(previous)
			handler := LoggingHandler(NewHTTPHandler(s.API, HTTPConfig{}))
			req := httptest.NewRequest(http.MethodPost, "/mcp/"+tc.token+"?secret=query-secret", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"server_info","arguments":{}}}`))
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if got := strings.Contains(rec.Body.String(), `"isError":true`); got != (tc.name == "rejected") {
				t.Fatalf("authentication result: %s", rec.Body.String())
			}
			if strings.Contains(logs.String(), tc.token) || strings.Contains(logs.String(), "query-secret") {
				t.Fatal("request log contains credential")
			}
			if !strings.Contains(logs.String(), "route=/mcp") || !strings.Contains(logs.String(), "status=") || !strings.Contains(logs.String(), "dur=") {
				t.Fatalf("missing safe request metadata: %s", logs.String())
			}
		})
	}
}

func TestLoggingHostileRequestMetadata(t *testing.T) {
	for _, method := range []string{http.MethodPost, "private-method"} {
		t.Run(method, func(t *testing.T) {
			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(previous)
			req := httptest.NewRequest(http.MethodPost, "/mcp/path-secret", strings.NewReader("private-body"))
			req.Method = method
			req.RemoteAddr = "private-remote"
			for _, header := range []string{"Mcp-Method", "MCP-Protocol-Version", "Authorization"} {
				req.Header.Set(header, "private-header\nforged-log")
			}
			rec := httptest.NewRecorder()
			LoggingHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusForbidden) })).ServeHTTP(rec, req)
			if strings.Contains(logs.String(), "private-") || strings.Contains(logs.String(), "forged-log") {
				t.Fatalf("untrusted metadata in log: %s", logs.String())
			}
			if !strings.Contains(logs.String(), "status=403") {
				t.Fatalf("missing status: %s", logs.String())
			}
		})
	}
}
