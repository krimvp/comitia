package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/krimvp/comitia/pkg/comitia"
	"github.com/krimvp/comitia/pkg/httpapi"
)

func TestClientCachesPositiveQuerySupportProbe(t *testing.T) {
	var mu sync.Mutex
	requests := map[string]int{}
	supported := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		ok := supported
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/v1/messages/query-support" && !ok:
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(comitia.ErrorInfo{Code: "not_found", Message: "not found"})
		case r.URL.Path == "/v1/messages/query-support":
			_, _ = io.WriteString(w, `{"ok":true}`)
		case r.URL.Path == "/v1/messages/get":
			_, _ = io.WriteString(w, `{"message":{"id":"m","context_id":"c","sequence":1}}`)
		default:
			_, _ = io.WriteString(w, `{"messages":[]}`)
		}
	}))
	defer server.Close()
	client, err := httpapi.NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	count := func(path string) int { mu.Lock(); defer mu.Unlock(); return requests["/v1/"+path] }
	// An older server is probed again rather than remembered as unsupported.
	for i := 0; i < 2; i++ {
		if _, err := client.GetMessage(ctx, "token", "c", "m"); !errors.Is(err, comitia.ErrInvalid) {
			t.Fatalf("older server: %v", err)
		}
	}
	if got := count("messages/query-support"); got != 2 || count("messages/get") != 0 {
		t.Fatalf("unsupported probes=%d gets=%d", got, count("messages/get"))
	}
	mu.Lock()
	supported = true
	mu.Unlock()
	for i := 0; i < 3; i++ {
		if _, err := client.GetMessage(ctx, "token", "c", "m"); err != nil {
			t.Fatal(err)
		}
		if _, err := client.ReplayMessages(ctx, "token", "c", 0, 10, comitia.MessageFilters{ThreadID: "m"}); err != nil {
			t.Fatal(err)
		}
		if _, err := client.WaitMessages(ctx, "token", "c", 0, 0, comitia.MessageFilters{ReplyTo: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	if got := count("messages/query-support"); got != 3 {
		t.Fatalf("query-support probes after support=%d, want 1", got-2)
	}
	if count("messages/get") != 3 || count("messages/replay") != 3 || count("messages/wait") != 3 {
		t.Fatalf("scoped reads: %v", requests)
	}
}
