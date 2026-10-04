package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
)

func TestServerInfoAndValidationHTTP(t *testing.T) {
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "local"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "local", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(svc)
	ts := httptest.NewServer(h)
	defer ts.Close()
	client, err := NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.ServerInfo(ctx, owner.Token)
	if err != nil || info.Version == "" || info.ProtocolVersion != comitia.ProtocolVersion || len(info.Features) == 0 {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if _, err := client.ServerInfo(ctx, "invalid-secret"); !errors.Is(err, comitia.ErrUnauthenticated) {
		t.Fatalf("auth=%v", err)
	}
	_, err = client.CreateTask(ctx, owner.Token, "unused", comitia.TaskInput{})
	if !errors.Is(err, comitia.ErrInvalid) || err.Error() != "invalid input: title required" {
		t.Fatalf("roundtrip=%v", err)
	}
	for _, tc := range []struct{ path, body, detail string }{
		{"tasks/create", `{"token":"secret-value"}`, `unknown field "token"`},
		{"tasks/create", `{}`, "task required"},
		{"tasks/create", `{"task":{"Title":false}}`, "title must have type string"},
		{"tasks/create", `{} {}`, "multiple JSON values"},
	} {
		r := httptest.NewRequest(http.MethodPost, "/v1/"+tc.path, strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+owner.Token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var response errorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if w.Code != http.StatusBadRequest || response.Code != "invalid" || !strings.Contains(response.Message, tc.detail) {
			t.Fatalf("status=%d response=%+v", w.Code, response)
		}
		if strings.Contains(response.Message, "secret-value") || strings.Contains(response.Message, owner.Token) {
			t.Fatalf("secret leaked: %v", response)
		}
		if err := decodeError(response.Code, response.Message); !errors.Is(err, comitia.ErrInvalid) || err.Error() != response.Message {
			t.Fatalf("decode=%v", err)
		}
	}
}
