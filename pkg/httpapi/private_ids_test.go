package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/sqlite"
)

func TestHTTPPrivateIDErrorsAreUniform(t *testing.T) {
	store, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "bootstrap", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, owner.Token, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	wc, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "private"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := svc.CreateTask(ctx, owner.Token, wc.ID, comitia.TaskInput{Title: "private"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := svc.CreatePrincipal(ctx, owner.Token, "outsider")
	if err != nil {
		t.Fatal(err)
	}
	outsider, err := svc.MintSession(ctx, owner.Token, p.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := svc.GrantContext(ctx, owner.Token, comitia.GrantInput{SessionID: owner.Session.ID, ContextID: wc.ID, Permissions: comitia.PermissionRead, ExpiresAt: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(svc)
	for _, resource := range []struct{ route, field, id string }{
		{"tasks/get", "task_id", task.ID},
		{"grants/revoke", "grant_id", grant.ID},
		{"sessions/revoke", "session_id", owner.Session.ID},
	} {
		t.Run(resource.route, func(t *testing.T) {
			for _, credential := range []string{"invalid", outsider.Token} {
				want := http.StatusUnauthorized
				if credential == outsider.Token {
					want = http.StatusForbidden
				}
				var previous map[string]any
				for _, id := range []string{resource.id, "missing"} {
					body, _ := json.Marshal(map[string]string{resource.field: id})
					request := httptest.NewRequest(http.MethodPost, "/v1/"+resource.route, bytes.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					request.Header.Set("Authorization", "Bearer "+credential)
					response := httptest.NewRecorder()
					handler.ServeHTTP(response, request)
					if response.Code != want {
						t.Fatalf("%q status=%d body=%s", id, response.Code, response.Body.String())
					}
					var value map[string]any
					if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
						t.Fatal(err)
					}
					delete(value, "request_id")
					if previous != nil && !reflect.DeepEqual(previous, value) {
						t.Fatalf("existence affects public diagnostics: %v / %v", previous, value)
					}
					previous = value
				}
			}
		})
	}
}
