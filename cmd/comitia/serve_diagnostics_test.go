package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/httpapi"
)

func captureDaemonLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	oldOutput, oldFlags := log.Writer(), log.Flags()
	var output bytes.Buffer
	log.SetOutput(&output)
	log.SetFlags(0)
	t.Cleanup(func() { log.SetOutput(oldOutput); log.SetFlags(oldFlags) })
	return &output
}

func TestServeStartupLog(t *testing.T) {
	output := captureDaemonLog(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := runServe(ctx, config{db: "test.db", credential: "credential-secret", bootstrapCredential: "bootstrap-secret"}, nil, nil, "serve", []string{"--listen", "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	line := output.String()
	for _, field := range []string{`comitia serve listen="127.0.0.1:`, `version="` + comitia.BuildVersion() + `"`, `revision="` + comitia.BuildIdentity().Revision + `"`, `db="test.db"`} {
		if !strings.Contains(line, field) {
			t.Errorf("missing %q in %s", field, line)
		}
	}
	if strings.Count(line, "\n") != 1 || strings.Contains(line, "secret") {
		t.Fatalf("startup log: %s", line)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	output.Reset()
	if err := serveHTTP(ctx, listener.Addr().String(), "test.db", http.NotFoundHandler()); err == nil {
		t.Fatal("occupied address accepted")
	}
	if output.Len() != 0 {
		t.Fatalf("logged startup before binding: %s", output.String())
	}
}

func TestServeRequestLogsAllowlistedMetadata(t *testing.T) {
	output := captureDaemonLog(t)
	handler := serveLoggingHandler(httpapi.NewHandler(nil))
	for _, tc := range []struct {
		method, path, route, loggedMethod string
		status                            int
	}{
		{"POST", "/v1/messages/append?token=query-secret", "/v1/send_message", "POST", 401},
		{"POST", "/v1/path-secret?token=query-secret", "unknown", "POST", 404},
		{"method-secret", "/v1/server_info", "/v1/server_info", "other", 405},
		{"GET", "/ui/missing-path-secret", "/ui/", "GET", 404},
	} {
		output.Reset()
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"claim_token":"body-secret"}`))
		if tc.status != 401 {
			req.Header.Set("Authorization", "Bearer header-secret")
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Request-ID", "request-secret")
		req.Header.Set("Cookie", "cookie-secret")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		line := output.String()
		for _, want := range []string{"method=" + tc.loggedMethod, "route=" + tc.route, "duration_ms=", "request_id=" + rec.Header().Get("X-Request-ID")} {
			if !strings.Contains(line, want) {
				t.Errorf("missing %q in %s", want, line)
			}
		}
		if rec.Code != tc.status || !strings.Contains(line, "status="+strconv.Itoa(tc.status)) || strings.Count(line, "\n") != 1 || strings.Contains(line, "secret") || strings.Contains(line, "claim_token") || strings.Contains(line, "Bearer") {
			t.Fatalf("response=%d log=%s", rec.Code, line)
		}
	}
	// A successful request also records the implicit 200 response.
	output.Reset()
	handler = serveLoggingHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("response-secret")) }))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/storage_status", nil))
	if !strings.Contains(output.String(), "status=200") || strings.Contains(output.String(), "secret") {
		t.Fatal(output.String())
	}
}

func TestServeLoggingFlags(t *testing.T) {
	for _, tc := range []struct {
		name, value      string
		args             []string
		enabled, invalid bool
	}{
		{name: "default"},
		{name: "env", value: "true", enabled: true},
		{name: "flag", args: []string{"--log-requests"}, enabled: true},
		{name: "override", value: "true", args: []string{"--log-requests=false"}},
		{name: "invalid override", value: "invalid", args: []string{"--log-requests=false"}},
		{name: "invalid env", value: "invalid", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("COMITIA_HTTP_LOG", tc.value)
			_, _, _, enabled, err := serveFlags(tc.args)
			if enabled != tc.enabled || (err != nil) != tc.invalid {
				t.Fatalf("enabled=%v err=%v", enabled, err)
			}
		})
	}
}

// An API implementation predating optional storage diagnostics still compiles.
type legacyCLIAPI struct{ comitia.API }

var _ comitia.API = legacyCLIAPI{}

func TestStorageStatusUnsupportedCLI(t *testing.T) {
	var output bytes.Buffer
	err := runServerInfo(context.Background(), config{}, legacyCLIAPI{}, json.NewEncoder(&output), "storage-status", nil)
	if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("legacy API: %v", err)
	}
	ts := httptest.NewServer(httpapi.NewHandler(legacyCLIAPI{}))
	defer ts.Close()
	err = commandErr(t, "--server-url", ts.URL, "--credential", "private-token", "storage-status")
	if !errors.Is(err, comitia.ErrInvalid) || !strings.Contains(err.Error(), "unsupported") || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("legacy remote: %v", err)
	}
}
