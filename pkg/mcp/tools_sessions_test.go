package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

func delegateArgs(ids ...string) map[string]any {
	grants := []map[string]any{}
	for _, id := range ids {
		grants = append(grants, map[string]any{"context_id": id, "permissions": []string{"read", "write", "grant"}, "ttl_seconds": 600})
	}
	return map[string]any{"name": "helper", "ttl_seconds": 1200, "grants": grants}
}

func TestDelegateCredentialFileAndRevocationTools(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(map[bool]string{false: "embedded", true: "remote"}[remote], func(t *testing.T) {
			s, svc, admin, parent := contextServer(t, remote)
			ctx := context.Background()
			child := callTool(t, s, "create_context", map[string]any{"parent_context_id": parent.ID, "name": "private"}).(comitia.WorkContext)
			result := callTool(t, s, "delegate_session", delegateArgs(parent.ID, child.ID)).(delegatedSessionResult)
			serialized, _ := json.Marshal(result)
			if bytes.Contains(bytes.ToLower(serialized), []byte("token")) {
				t.Fatal("result has a token field")
			}
			info, err := os.Stat(result.CredentialFile)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0600 {
				t.Fatalf("file mode=%o", info.Mode().Perm())
			}
			dirInfo, err := os.Stat(s.SessionsDir)
			if err != nil || dirInfo.Mode().Perm() != 0700 {
				t.Fatalf("directory: %v", err)
			}
			if filepath.Base(result.CredentialFile) != result.SessionID+".json" || len(result.GrantIDs) != 2 {
				t.Fatal("result metadata")
			}
			data, err := os.ReadFile(result.CredentialFile)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Principal  comitia.Principal  `json:"principal"`
				Credential comitia.Credential `json:"credential"`
				Grants     []comitia.Grant    `json:"grants"`
				ServerURL  string             `json:"server_url"`
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			if doc.Credential.Token == "" || bytes.Contains(serialized, []byte(doc.Credential.Token)) {
				t.Fatal("credential missing from file or leaked to result")
			}
			if doc.Principal.ID != result.PrincipalID || doc.Credential.Session.ID != result.SessionID || !doc.Credential.Session.ExpiresAt.Equal(result.ExpiresAt) || doc.ServerURL != s.ServerURL || len(doc.Grants) != 2 {
				t.Fatal("credential file metadata")
			}
			for i, g := range doc.Grants {
				if g.ID != result.GrantIDs[i] {
					t.Fatal("grant IDs")
				}
			}
			if _, err := svc.GetContext(ctx, doc.Credential.Token, child.ID); err != nil {
				t.Fatal(err)
			}
			assertPythonCredential(t, result.CredentialFile, s.ServerURL)
			// Invite an unrelated session already belonging to this workspace.
			_, target, _, err := svc.DelegateSession(ctx, admin.Token, comitia.DelegateInput{PrincipalName: "invitee", TTL: time.Hour, Grants: []comitia.DelegatedGrant{{ContextID: parent.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(30 * time.Minute)}}})
			if err != nil {
				t.Fatal(err)
			}
			invite := callTool(t, s, "grant_context", map[string]any{"session_id": target.Session.ID, "context_id": child.ID, "permissions": []string{"read"}, "ttl_seconds": 300}).(comitia.Grant)
			if _, err := svc.GetContext(ctx, target.Token, child.ID); err != nil {
				t.Fatal(err)
			}
			resultGrant := callTool(t, s, "revoke_grant", map[string]any{"grant_id": invite.ID}).(map[string]bool)
			if !resultGrant["ok"] {
				t.Fatal("revoke result")
			}
			if _, err := svc.GetContext(ctx, target.Token, child.ID); !errors.Is(err, comitia.ErrForbidden) {
				t.Fatalf("invite revoke: %v", err)
			}
			// The helper can delegate again, but its parent cannot revoke a grandchild session directly.
			helper := &Server{API: s.API, Credential: doc.Credential.Token, SessionsDir: s.SessionsDir, ServerURL: s.ServerURL}
			grandchild := callTool(t, helper, "delegate_session", map[string]any{"name": "nested", "ttl_seconds": 300, "grants": []any{map[string]any{"context_id": child.ID, "permissions": []string{"read"}, "ttl_seconds": 120}}}).(delegatedSessionResult)
			raw, _ := json.Marshal(map[string]any{"session_id": grandchild.SessionID})
			if _, err := s.call(ctx, "revoke_session", raw); !errors.Is(err, comitia.ErrForbidden) {
				t.Fatalf("grandchild revoke: %v", err)
			}
			callTool(t, s, "revoke_session", map[string]any{"session_id": result.SessionID})
			if _, err := svc.GetContext(ctx, doc.Credential.Token, child.ID); !errors.Is(err, comitia.ErrUnauthenticated) {
				t.Fatalf("session revoke: %v", err)
			}
		})
	}
}

func assertPythonCredential(t *testing.T, path, serverURL string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	// Exercise the actual wrapper, intercepting execve without printing secrets.
	code := `import importlib.util,json,pathlib,sys,tempfile
spec=importlib.util.spec_from_file_location('client',sys.argv[1]); client=importlib.util.module_from_spec(spec); spec.loader.exec_module(client)
path=pathlib.Path(sys.argv[2]); endpoint=sys.argv[3]; doc=client.load(path)
assert doc['credential']['Token'] and doc['server_url']==endpoint
with tempfile.TemporaryDirectory() as temp:
 root=pathlib.Path(temp); (root/'local-context.json').write_text(json.dumps({'server_url':'http://wrong.example'}))
 calls=[]
 client.os.execve=lambda binary,args,env: calls.append((binary,args,env))
 client.main(['--credential-file',str(path),'context-list'],root)
 assert len(calls)==1
 _,args,env=calls[0]
 assert args[1:]==['context-list']
 assert env['COMITIA_CREDENTIAL']==doc['credential']['Token']
 assert env['COMITIA_SERVER_URL']==endpoint
`
	cmd := exec.Command(python, "-c", code, "../../scripts/comitia_client.py", path, serverURL)
	cmd.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("credential wrapper: %v %s", err, out)
	}
}

type delegationAPI struct {
	comitia.API
	calls   int
	revoked string
}

func (f *delegationAPI) DelegateSession(context.Context, string, comitia.DelegateInput) (comitia.Principal, comitia.Credential, []comitia.Grant, error) {
	f.calls++
	return comitia.Principal{ID: "principal"}, comitia.Credential{Token: "never-show-this-token", Session: comitia.Session{ID: strings.Repeat("a", 32), ExpiresAt: time.Now().Add(time.Hour)}}, []comitia.Grant{{ID: "grant"}}, nil
}
func (f *delegationAPI) RevokeSession(_ context.Context, _ string, id string) error {
	f.revoked = id
	return nil
}

func TestDelegateNoTokenInProtocolAndExclusiveFailureCleanup(t *testing.T) {
	api := &delegationAPI{}
	s := &Server{API: api, Credential: "fixed-secret", SessionsDir: filepath.Join(t.TempDir(), "sessions"), ServerURL: "http://127.0.0.1:8390"}
	raw, _ := json.Marshal(map[string]any{"name": "delegate_session", "arguments": delegateArgs("context")})
	r := s.handle(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: raw})
	output, _ := json.Marshal(r)
	if bytes.Contains(output, []byte("never-show-this-token")) || bytes.Contains(bytes.ToLower(output), []byte(`"token"`)) || bytes.Contains(output, []byte("fixed-secret")) {
		t.Fatal("protocol leaked token")
	}
	path := filepath.Join(s.SessionsDir, strings.Repeat("a", 32)+".json")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r = s.handle(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage("2"), Method: "tools/call", Params: raw})
	output, _ = json.Marshal(r)
	if !bytes.Contains(output, []byte("internal error")) || bytes.Contains(output, []byte("never-show-this-token")) {
		t.Fatal("failed handoff not sanitized")
	}
	second, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(first, second) || api.revoked != strings.Repeat("a", 32) {
		t.Fatal("exclusive file overwritten or failed session not revoked")
	}
}

func TestSessionsDirectoryDefaultsAndPrivacy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root, err := privateSessionsDir("")
	if err != nil {
		t.Fatal(err)
	}
	root.Close()
	dir := filepath.Join(home, ".local", "share", "comitia", "sessions")
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("default directory mode")
	}
	unsafe := filepath.Join(home, "unsafe")
	if err := os.Mkdir(unsafe, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := privateSessionsDir(unsafe); err == nil {
		t.Fatal("accepted public directory")
	}
	link := filepath.Join(home, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if _, err := privateSessionsDir(link); err == nil {
		t.Fatal("accepted symlink directory")
	}
	for _, configured := range []string{unsafe, link} {
		api := &delegationAPI{}
		s := &Server{API: api, Credential: "secret", SessionsDir: configured, ServerURL: "http://127.0.0.1:8390"}
		raw, _ := json.Marshal(delegateArgs("context"))
		if _, err := s.call(context.Background(), "delegate_session", raw); err == nil || api.calls != 0 {
			t.Fatal("delegated into unsafe directory")
		}
	}
}

func TestHTTPDelegationWritesPrivateCredential(t *testing.T) {
	s, _, _, parent := contextServer(t, true)
	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousWriter)
	handler := LoggingHandler(NewHTTPHandler(s.API, HTTPConfig{SessionsDir: s.SessionsDir, ServerURL: s.ServerURL, AllowDelegation: true}))
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "delegate_session", "arguments": delegateArgs(parent.ID)}})
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+s.Credential)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP status=%d", rec.Code)
	}
	var reply struct {
		Result struct {
			IsError           bool `json:"isError"`
			StructuredContent struct {
				Result delegatedSessionResult `json:"result"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
		t.Fatal(err)
	}
	result := reply.Result.StructuredContent.Result
	if reply.Result.IsError || result.SessionID == "" {
		t.Fatal("HTTP delegation failed")
	}
	if bytes.Contains(bytes.ToLower(rec.Body.Bytes()), []byte(`"token"`)) {
		t.Fatal("HTTP token field")
	}
	data, err := os.ReadFile(result.CredentialFile)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Credential comitia.Credential `json:"credential"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Credential.Token == "" || bytes.Contains(rec.Body.Bytes(), []byte(document.Credential.Token)) || bytes.Contains(logs.Bytes(), []byte(document.Credential.Token)) || bytes.Contains(logs.Bytes(), []byte(s.Credential)) {
		t.Fatal("HTTP response or request log exposed a token")
	}
	assertPythonCredential(t, result.CredentialFile, s.ServerURL)
}

func TestDelegateRequiresUsableDaemonURL(t *testing.T) {
	for _, url := range []string{"", "relative", "file:///tmp/state", "http://user:secret@localhost", "https://"} {
		api := &delegationAPI{}
		s := &Server{API: api, Credential: "secret", ServerURL: url, SessionsDir: filepath.Join(t.TempDir(), "sessions")}
		raw, _ := json.Marshal(delegateArgs("context"))
		if _, err := s.call(context.Background(), "delegate_session", raw); !errors.Is(err, comitia.ErrInvalid) || api.calls != 0 {
			t.Fatalf("daemon URL %q: %v", url, err)
		}
	}
}

func TestCredentialFilenameRejectsTraversalAndSymlinks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "sessions")
	root, err := privateSessionsDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, id := range []string{"../outside", "", strings.Repeat("g", 32), strings.Repeat("a", 30)} {
		if _, err := writeSessionCredential(root, "http://localhost", comitia.Principal{}, comitia.Credential{Session: comitia.Session{ID: id}}, nil); err == nil {
			t.Fatal("accepted invalid filename")
		}
	}
	outside := filepath.Join(t.TempDir(), "untouched")
	if err := os.WriteFile(outside, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("b", 32)
	if err := os.Symlink(outside, filepath.Join(dir, id+".json")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeSessionCredential(root, "http://localhost", comitia.Principal{}, comitia.Credential{Token: "private", Session: comitia.Session{ID: id}}, nil); err == nil {
		t.Fatal("followed credential symlink")
	}
	content, err := os.ReadFile(outside)
	if err != nil || string(content) != "original" {
		t.Fatal("symlink target changed")
	}
}

type failingRevokeAPI struct{ delegationAPI }

func (f *failingRevokeAPI) RevokeSession(context.Context, string, string) error {
	return errors.New("daemon unavailable")
}

func TestFailedHandoffAndCleanupReportsSessionID(t *testing.T) {
	api := &failingRevokeAPI{}
	dir := filepath.Join(t.TempDir(), "sessions")
	s := &Server{API: api, Credential: "fixed-secret", SessionsDir: dir, ServerURL: "http://127.0.0.1:8390"}
	id := strings.Repeat("a", 32)
	// An existing file with the session ID makes the exclusive write fail.
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(previousWriter)
	raw, _ := json.Marshal(map[string]any{"name": "delegate_session", "arguments": delegateArgs("context")})
	r := s.handle(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call", Params: raw})
	output, _ := json.Marshal(r)
	if !bytes.Contains(output, []byte(id)) || !bytes.Contains(output, []byte("revoke_session")) || !bytes.Contains(output, []byte(`"isError":true`)) {
		t.Fatalf("session ID not reported: %s", output)
	}
	if !bytes.Contains(logs.Bytes(), []byte(id)) {
		t.Fatal("session ID not logged")
	}
	for _, secret := range []string{"never-show-this-token", "fixed-secret"} {
		if bytes.Contains(output, []byte(secret)) || bytes.Contains(logs.Bytes(), []byte(secret)) {
			t.Fatal("secret leaked")
		}
	}
}

func TestSessionsDirectoryRefusesNonDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0700); err != nil {
		t.Fatal(err)
	}
	if root, err := privateSessionsDir(path); err == nil {
		root.Close()
		t.Fatal("accepted a file as sessions directory")
	}
}

func TestHTTPDelegationDisabledByDefault(t *testing.T) {
	api := &delegationAPI{}
	dir := filepath.Join(t.TempDir(), "sessions")
	handler := NewHTTPHandler(api, HTTPConfig{SessionsDir: dir, ServerURL: "http://127.0.0.1:8390"})
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "delegate_session", "arguments": delegateArgs("context")}})
	rec := post(t, handler, string(raw), map[string]string{"Authorization": "Bearer good"})
	if rec.Code != http.StatusOK || !toolError(t, rec.Body.String()) || !strings.Contains(rec.Body.String(), "--allow-delegation") {
		t.Fatalf("delegation not refused: %d %s", rec.Code, rec.Body.String())
	}
	if api.calls != 0 {
		t.Fatal("refused delegation reached the API")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("refused delegation created the sessions directory")
	}
	listed := func(h http.Handler) map[string]bool {
		rec := post(t, h, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, map[string]string{"Authorization": "Bearer good"})
		var reply struct {
			Result struct {
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"result"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
			t.Fatal(err)
		}
		names := map[string]bool{}
		for _, tool := range reply.Result.Tools {
			names[tool.Name] = true
		}
		return names
	}
	if names := listed(handler); names["delegate_session"] || !names["revoke_session"] || len(names) != len(tools())-1 {
		t.Fatalf("disabled tools/list: %v", names)
	}
	if names := listed(NewHTTPHandler(api, HTTPConfig{AllowDelegation: true})); !names["delegate_session"] {
		t.Fatal("allowed delegation not listed")
	}
	// The stdio server is a local launcher and keeps delegation.
	stdio := &Server{API: api, Credential: "good"}
	r := stdio.handle(context.Background(), request{JSONRPC: "2.0", ID: json.RawMessage("3"), Method: "tools/list"})
	if b, _ := json.Marshal(r); !bytes.Contains(b, []byte(`"delegate_session"`)) {
		t.Fatal("stdio server hid delegate_session")
	}
}
