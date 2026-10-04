package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
)

func TestUnknownRouteClassification(t *testing.T) {
	for _, path := range []string{"/outside", "/v1/unknown"} {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		r.Header.Set("Authorization", "Bearer credential")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		NewHandler(fakeAPI{}).ServeHTTP(w, r)
		var e errorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		if w.Code != 404 || e.Code != "not_found" || !errors.Is(decodeError(e.Code, e.Message), comitia.ErrNotFound) {
			t.Fatalf("status=%d error=%+v", w.Code, e)
		}
	}
	ts := httptest.NewServer(NewHandler(fakeAPI{}))
	defer ts.Close()
	c, _ := NewClient(ts.URL, ts.Client())
	var out emptyResult
	if err := c.call(context.Background(), "credential", "unknown", emptyRequest{}, &out); !errors.Is(err, comitia.ErrNotFound) {
		t.Fatalf("client error=%v", err)
	}
}

type unavailableWrites struct {
	comitia.Store
	fail bool
}

func (s *unavailableWrites) Update(ctx context.Context, fn func(comitia.WriteStore) error) error {
	if s.fail {
		return errors.New("private-storage-path private-secret")
	}
	return s.Store.Update(ctx, fn)
}

func TestStorageReadinessSeparateFromCapabilities(t *testing.T) {
	st, err := comitia.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	store := &unavailableWrites{Store: st}
	svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	_, owner, err := svc.Bootstrap(context.Background(), comitia.BootstrapInput{Credential: "bootstrap", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	store.fail = true
	if _, err := svc.ServerInfo(context.Background(), owner.Token); err != nil {
		t.Fatal(err)
	}
	h := NewHandler(svc)
	r := httptest.NewRequest(http.MethodPost, "/v1/storage_status", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer "+owner.Token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got struct {
		Storage struct {
			WriteReady bool   `json:"write_ready"`
			Failures   int    `json:"write_failures"`
			Class      string `json:"last_write_error_class"`
		} `json:"storage_status"`
	}
	if json.Unmarshal(w.Body.Bytes(), &got) != nil || w.Code != 200 || got.Storage.WriteReady || got.Storage.Failures < 1 || got.Storage.Class != "internal" {
		t.Fatalf("status=%d response=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "private-") {
		t.Fatalf("leak: %s", w.Body.String())
	}
}

func TestInternalFailureDiagnostic(t *testing.T) {
	var logs bytes.Buffer
	old := log.Writer()
	log.SetOutput(&logs)
	defer log.SetOutput(old)
	h := NewHandler(failedAppend{})
	r := httptest.NewRequest(http.MethodPost, "/v1/messages/append", strings.NewReader(`{"context_id":"private-context","message":{"DedupeKey":"private-key","Body":"private-body"}}`))
	r.Header.Set("Authorization", "Bearer private-token")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Request-ID", "private-client-id")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	id, _ := got["request_id"].(string)
	if w.Code != 500 || id == "" || !strings.Contains(logs.String(), id) || !strings.Contains(logs.String(), "send_message") || !strings.Contains(logs.String(), "internal") {
		t.Fatalf("response=%s logs=%s", w.Body.String(), logs.String())
	}
	for _, secret := range []string{"private-context", "private-key", "private-body", "private-token", "private-client-id", "private-error"} {
		if strings.Contains(logs.String()+w.Body.String(), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
}

type failedAppend struct{ comitia.API }

func (failedAppend) AppendMessage(context.Context, string, string, comitia.MessageInput) (comitia.Message, error) {
	return comitia.Message{}, errors.New("private-error")
}

func TestClientPreservesOperationalMetadata(t *testing.T) {
	var diagnostic comitia.Diagnostic
	ts := httptest.NewServer(NewHandlerWithDiagnostics(failedAppend{}, func(d comitia.Diagnostic) { diagnostic = d }))
	defer ts.Close()
	client, err := NewClient(ts.URL, ts.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.AppendMessage(context.Background(), "private-credential", "private-context", comitia.MessageInput{DedupeKey: "private-key", Body: "private-body"})
	var operational *comitia.OperationalError
	if !errors.As(err, &operational) || operational.Info.RequestID != diagnostic.RequestID || operational.Info.Hint == "" || operational.Info.Code != "internal" || !strings.Contains(err.Error(), "request_id") {
		t.Fatalf("err=%v diagnostic=%+v", err, diagnostic)
	}
}
