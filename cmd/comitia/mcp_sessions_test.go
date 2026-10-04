package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
)

func TestMCPSessionsDirectoryFlagsAndEnvironment(t *testing.T) {
	for _, useFlag := range []bool{false, true} {
		t.Run(map[bool]string{false: "environment", true: "flag"}[useFlag], func(t *testing.T) {
			store, err := sqlite.OpenSQLite(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "once"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			_, admin, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "once", AdminName: "owner", SessionTTL: time.Hour})
			if err != nil {
				t.Fatal(err)
			}
			ws, err := svc.CreateWorkspace(ctx, admin.Token, "workspace")
			if err != nil {
				t.Fatal(err)
			}
			c, err := svc.CreateContext(ctx, admin.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "context"})
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(t.TempDir(), "sessions")
			t.Setenv("COMITIA_SESSIONS_DIR", dir)
			args := []string{"--credential-server-url", "http://127.0.0.1:8390"}
			if useFlag {
				t.Setenv("COMITIA_SESSIONS_DIR", filepath.Join(t.TempDir(), "unused"))
				args = append(args, "--sessions-dir", dir)
			}
			in, err := os.CreateTemp(t.TempDir(), "input")
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			encoder := json.NewEncoder(in)
			if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-06-18"}}); err != nil {
				t.Fatal(err)
			}
			if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "delegate_session", "arguments": map[string]any{"name": "helper", "ttl_seconds": 600, "grants": []any{map[string]any{"context_id": c.ID, "permissions": []string{"read"}, "ttl_seconds": 300}}}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := in.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			out, err := os.CreateTemp(t.TempDir(), "output")
			if err != nil {
				t.Fatal(err)
			}
			defer out.Close()
			originalIn, originalOut := os.Stdin, os.Stdout
			os.Stdin, os.Stdout = in, out
			defer func() { os.Stdin, os.Stdout = originalIn, originalOut }()
			err = runServe(ctx, config{credential: admin.Token}, svc, json.NewEncoder(out), "mcp", args)
			os.Stdin, os.Stdout = originalIn, originalOut
			if err != nil {
				t.Fatal(err)
			}
			if _, err := out.Seek(0, 0); err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(out)
			var initReply map[string]any
			if err := decoder.Decode(&initReply); err != nil {
				t.Fatal(err)
			}
			var reply struct {
				Result struct {
					IsError           bool `json:"isError"`
					StructuredContent struct {
						Result struct {
							CredentialFile string `json:"credential_file"`
						} `json:"result"`
					} `json:"structuredContent"`
				} `json:"result"`
			}
			if err := decoder.Decode(&reply); err != nil {
				t.Fatal(err)
			}
			path := reply.Result.StructuredContent.Result.CredentialFile
			if reply.Result.IsError || filepath.Dir(path) != dir {
				t.Fatal("MCP credential directory flag/env not applied")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var document struct {
				ServerURL string `json:"server_url"`
			}
			if err := json.Unmarshal(data, &document); err != nil || document.ServerURL != "http://127.0.0.1:8390" {
				t.Fatal("embedded credential URL flag not applied")
			}
		})
	}
}

func TestMCPHTTPDelegationOptIn(t *testing.T) {
	for _, tc := range []struct {
		name, env string
		args      []string
		listed    bool
	}{
		{"default", "", nil, false},
		{"flag", "", []string{"--allow-delegation", "--log-requests"}, true},
		{"environment", "1", nil, true},
		{"true", "true", nil, true},
		{"True", "True", nil, true},
		{"TRUE", "TRUE", nil, true},
		{"t", "t", nil, true},
		{"T", "T", nil, true},
		{"False", "False", nil, false},
		{"FALSE", "FALSE", nil, false},
		{"f", "f", nil, false},
		{"F", "F", nil, false},
		{"flag-true", "false", []string{"--allow-delegation", "--log-requests"}, true},
		{"false", "false", nil, false},
		{"zero", "0", nil, false},
		{"flag-false", "true", []string{"--allow-delegation=false", "--log-requests=false"}, false},
		{"flag-over-invalid-env", "invalid", []string{"--allow-delegation=false", "--log-requests=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("COMITIA_CREDENTIAL", "")
			t.Setenv("COMITIA_MCP_HTTP_ALLOW_DELEGATION", tc.env)
			t.Setenv("COMITIA_MCP_HTTP_LOG", tc.env)
			var logs bytes.Buffer
			previous := log.Writer()
			log.SetOutput(&logs)
			defer log.SetOutput(previous)
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			addr := l.Addr().String()
			l.Close()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- runServe(ctx, config{serverURL: "http://127.0.0.1:8390"}, nil, nil, "mcp-http", append([]string{"--listen", addr}, tc.args...))
			}()
			defer func() {
				cancel()
				<-done
				if got := strings.Contains(logs.String(), "mcp-http"); got != tc.listed {
					t.Errorf("request logging enabled=%v, want %v", got, tc.listed)
				}
			}()
			var body []byte
			for deadline := time.Now().Add(5 * time.Second); ; {
				req, _ := http.NewRequest(http.MethodPost, "http://"+addr+"/mcp", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)))
				req.Header.Set("Authorization", "Bearer any")
				res, err := http.DefaultClient.Do(req)
				if err == nil {
					var buf bytes.Buffer
					_, _ = buf.ReadFrom(res.Body)
					res.Body.Close()
					body = buf.Bytes()
					break
				}
				if time.Now().After(deadline) {
					t.Fatal(err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			if got := bytes.Contains(body, []byte(`"delegate_session"`)); got != tc.listed || !bytes.Contains(body, []byte(`"revoke_session"`)) {
				t.Fatalf("delegate_session listed=%v: %s", got, body)
			}
		})
	}
}

func TestMCPHTTPInvalidBooleanEnvironment(t *testing.T) {
	for _, name := range []string{"COMITIA_MCP_HTTP_ALLOW_DELEGATION", "COMITIA_MCP_HTTP_LOG"} {
		for _, value := range []string{"yes", "no", "2", " false ", "private-invalid-value"} {
			t.Run(name+"/"+value, func(t *testing.T) {
				t.Setenv("COMITIA_CREDENTIAL", "")
				t.Setenv("COMITIA_MCP_HTTP_ALLOW_DELEGATION", "")
				t.Setenv("COMITIA_MCP_HTTP_LOG", "")
				t.Setenv(name, value)
				err := runServe(context.Background(), config{serverURL: "http://localhost:8390"}, nil, nil, "mcp-http", []string{"--listen", "127.0.0.1:invalid-port"})
				if !errors.Is(err, comitia.ErrInvalid) {
					t.Fatalf("invalid boolean must fail before listening: %v", err)
				}
				if strings.Contains(err.Error(), value) {
					t.Fatal("invalid setting value exposed")
				}
			})
		}
	}
}
