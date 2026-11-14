package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/httpapi/internal/legacy"
	"github.com/krimvp/comitia/sqlite"
)

// sqliteCode is a storage error carrying a SQLite primary result code, as
// the driver reports it.
type sqliteCode int

func (c sqliteCode) Error() string { return "private-storage-path" }
func (c sqliteCode) Code() int     { return int(c) }

type codedWrites struct {
	comitia.Store
	err error
}

func (s *codedWrites) Update(ctx context.Context, fn func(comitia.WriteStore) error) error {
	if s.err != nil {
		return s.err
	}
	return s.Store.Update(ctx, fn)
}

// TestStorageFailureClassCrossesHTTP checks that a classified storage failure
// keeps its class through the Handler and Client, that the status and the
// rest of the body stay what the frozen legacy Handler sends, and that the
// legacy Client reads the body exactly as before.
func TestStorageFailureClassCrossesHTTP(t *testing.T) {
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	store := &codedWrites{Store: st}
	svc, err := comitia.New(comitia.Config{Store: store, BootstrapCredential: "bootstrap"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "bootstrap", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, owner.Token, "w")
	if err != nil {
		t.Fatal(err)
	}
	wc, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "c"})
	if err != nil {
		t.Fatal(err)
	}
	current := httptest.NewServer(NewHandler(svc))
	defer current.Close()
	frozen := httptest.NewServer(legacy.NewHandler(svc))
	defer frozen.Close()

	post := func(base string) (int, map[string]any) {
		r, _ := http.NewRequest(http.MethodPost, base+"/v1/tasks/create", strings.NewReader(fmt.Sprintf(`{"context_id":%q,"task":{"Title":"t"}}`, wc.ID)))
		r.Header.Set("Authorization", "Bearer "+owner.Token)
		r.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		delete(body, "request_id")
		return resp.StatusCode, body
	}

	for _, tc := range []struct {
		code  int
		class string
	}{{5, "storage_busy"}, {6, "storage_busy"}, {5 | 2<<8, "storage_busy"}, {8, "storage_read_only"}, {10, "storage_io"}, {13, "storage_full"}, {1, "internal"}} {
		t.Run(tc.class+fmt.Sprint(tc.code), func(t *testing.T) {
			store.err = fmt.Errorf("private-wrap: %w", sqliteCode(tc.code))
			defer func() { store.err = nil }()

			status, body := post(current.URL)
			oldStatus, oldBody := post(frozen.URL)
			class, _ := body["failure_class"].(string)
			delete(body, "failure_class")
			if status != oldStatus || fmt.Sprint(body) != fmt.Sprint(oldBody) {
				t.Fatalf("body changed: %d %v, legacy %d %v", status, body, oldStatus, oldBody)
			}
			if want := map[bool]string{true: "", false: tc.class}[tc.class == "internal"]; class != want {
				t.Fatalf("failure_class=%q, want %q", class, want)
			}

			client, _ := NewClient(current.URL, current.Client())
			_, err := client.CreateTask(ctx, owner.Token, wc.ID, comitia.TaskInput{Title: "t"})
			oldClient, _ := legacy.NewClient(current.URL, current.Client())
			_, oldErr := oldClient.CreateTask(ctx, owner.Token, wc.ID, comitia.TaskInput{Title: "t"})
			if got := comitia.FailureClass(err); got != tc.class {
				t.Fatalf("client FailureClass=%q, want %q (%v)", got, tc.class, err)
			}
			if comitia.ErrorClass(err) != "internal" || comitia.FailureClass(oldErr) != "internal" || withoutRequestID(err) != withoutRequestID(oldErr) {
				t.Fatalf("client error=%v legacy client error=%v", err, oldErr)
			}
			if cause := errors.Unwrap(err); cause == nil || cause.Error() != "remote error: internal server error" {
				t.Fatalf("cause=%v", cause)
			}
		})
	}
}

// TestOlderServerErrorUnchanged checks that an error body without
// failure_class, as older servers send, and one with an unknown class, yield
// exactly the error the client returned before failure_class existed.
func TestOlderServerErrorUnchanged(t *testing.T) {
	for _, body := range []string{
		`{"code":"internal","message":"internal server error","hint":"h","request_id":"0123456789abcdef0123456789abcdef"}`,
		`{"code":"internal","message":"internal server error","hint":"h","request_id":"0123456789abcdef0123456789abcdef","failure_class":"storage_unheard_of"}`,
		`{"code":"not_found","message":"not found","request_id":"0123456789abcdef0123456789abcdef","failure_class":"storage_busy"}`,
	} {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(body))
		}))
		client, _ := NewClient(ts.URL, ts.Client())
		var out emptyResult
		err := client.call(context.Background(), "credential", "tasks/get", emptyRequest{}, &out)
		ts.Close()
		var e comitia.ErrorInfo
		json.Unmarshal([]byte(body), &e)
		want := &comitia.OperationalError{Info: e, Cause: decodeError(e.Code, e.Message)}
		var got *comitia.OperationalError
		if !errors.As(err, &got) || got.Info.String() != want.Info.String() || fmt.Sprintf("%#v", got.Cause) != fmt.Sprintf("%#v", want.Cause) || comitia.FailureClass(err) != comitia.FailureClass(want) {
			t.Fatalf("body %s: err=%#v, want %#v", body, err, want)
		}
	}
}

func withoutRequestID(err error) string {
	s := fmt.Sprint(err)
	if i := strings.Index(s, "(request_id:"); i >= 0 {
		return s[:i]
	}
	return s
}
