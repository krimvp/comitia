package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/krimvp/comitia"
	"github.com/krimvp/comitia/sqlite"
)

func TestRemoteReplayAndWaitBytePages(t *testing.T) {
	st, err := sqlite.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc, err := comitia.New(comitia.Config{Store: st, BootstrapCredential: "boot"})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, owner, err := svc.Bootstrap(ctx, comitia.BootstrapInput{Credential: "boot", AdminName: "owner", SessionTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := svc.CreateWorkspace(ctx, owner.Token, "workspace")
	if err != nil {
		t.Fatal(err)
	}
	wc, err := svc.CreateContext(ctx, owner.Token, comitia.CreateContextInput{WorkspaceID: ws.ID, Name: "context"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(NewHandler(svc))
	defer ts.Close()
	client, err := NewClientWithInstanceDefaults(ts.URL, ts.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, err := client.AppendMessage(ctx, owner.Token, wc.ID, comitia.MessageInput{DedupeKey: fmt.Sprint(i), Body: strings.Repeat("x", 500000)})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, wait := range []bool{false, true} {
		t.Run(fmt.Sprintf("wait_%v", wait), func(t *testing.T) {
			var messages []comitia.Message
			if wait {
				messages, err = client.WaitMessages(ctx, owner.Token, wc.ID, 0, 0, comitia.MessageFilters{MaxBytes: comitia.MaxMessageBytes})
			} else {
				messages, err = client.ReplayMessages(ctx, owner.Token, wc.ID, 0, 100, comitia.MessageFilters{MaxBytes: comitia.MaxMessageBytes})
			}
			if err != nil || len(messages) != 3 {
				t.Fatalf("wait=%v: remote aggregate: count=%d err=%v", wait, len(messages), err)
			}
			after := int64(0)
			for _, want := range []int{2, 1, 0} {
				var page comitia.MessagePage
				if wait {
					page, err = client.WaitMessagesPage(ctx, owner.Token, wc.ID, after, 0)
				} else {
					page, err = client.ReplayMessagesPage(ctx, owner.Token, wc.ID, after, 100)
				}
				if err != nil || len(page.Messages) != want || page.HasMore != (want == 2) || page.Messages == nil {
					t.Fatalf("wait=%v want=%d: page count=%d more=%v err=%v", wait, want, len(page.Messages), page.HasMore, err)
				}
				if want > 0 && page.NextAfter <= after {
					t.Fatal("no continuation progress")
				}
				after = page.NextAfter
			}
		})
	}
	// A legal near-request-limit message still fits the independent response cap,
	// and a tiny budget returns it whole rather than stranding the reader.
	big, err := client.AppendMessage(ctx, owner.Token, wc.ID, comitia.MessageInput{DedupeKey: "big", Body: strings.Repeat("z", maxRequestBody-512)})
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ReplayMessagesPage(ctx, owner.Token, wc.ID, 3, 100, comitia.MessageFilters{MaxBytes: 1})
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Body != big.Body || page.NextAfter != big.Sequence {
		t.Fatalf("largest-message progress: count=%d err=%v", len(page.Messages), err)
	}
}

func TestHTTPClientResponseBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       string
	}{
		{"exact_response_cap", `{"messages":[]}` + strings.Repeat(" ", maxResponseBody-len(`{"messages":[]}`)), ""},
		{"larger_than_request", `{"messages":[{"Body":"` + strings.Repeat("x", maxRequestBody) + `"}]}`, ""},
		{"oversize", `{"messages":[{"Body":"` + strings.Repeat("x", maxResponseBody) + `"}]}`, "oversize"},
		{"oversize_after_valid_json", `{}` + strings.Repeat(" ", maxResponseBody), "oversize"},
		{"trailing_value", `{} {}`, "trailing"},
		{"truncated", `{"messages":[`, "decode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, tc.body) }))
			defer ts.Close()
			client, err := NewClientWithInstanceDefaults(ts.URL, ts.Client(), comitia.InstanceDefaults{})
			if err != nil {
				t.Fatal(err)
			}
			err = client.call(context.Background(), "token", "messages/replay", emptyRequest{}, &struct {
				Messages []comitia.Message `json:"messages"`
			}{})
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if tc.want == "oversize" {
				if !errors.Is(err, ErrResponseTooLarge) {
					t.Fatalf("wanted explicit oversize error: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %s: %v", tc.want, err)
			}
		})
	}
}

func TestOversizedRemoteResponseAdvisesSmallerRequest(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"tasks":[`+strings.Repeat(" ", maxResponseBody)+`]}`)
	}))
	defer ts.Close()
	client, err := NewClientWithInstanceDefaults(ts.URL, ts.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = client.ListTasks(ctx, "token", "ctx", "", 1000)
	info := comitia.SafeError("list_tasks", err, "")
	if !errors.Is(err, ErrResponseTooLarge) || info.Code != "invalid" || !strings.Contains(info.Message, "lower limit") || strings.Contains(info.Message, "max_bytes") || info.Hint != "" {
		t.Fatalf("task list: err=%v info=%+v", err, info)
	}
	_, err = client.ReplayMessagesPage(ctx, "token", "ctx", 0, 100, comitia.MessageFilters{})
	if !errors.Is(err, ErrResponseTooLarge) || comitia.ErrorClass(err) != "invalid" || !strings.Contains(err.Error(), "smaller max_bytes") {
		t.Fatalf("message replay: %v", err)
	}
}

func TestHTTPClientRequestBudget(t *testing.T) {
	calls := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; json.NewEncoder(w).Encode(emptyResult{}) }))
	defer ts.Close()
	client, err := NewClientWithInstanceDefaults(ts.URL, ts.Client(), comitia.InstanceDefaults{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.AppendMessage(context.Background(), "token", "context", comitia.MessageInput{DedupeKey: "large", Body: strings.Repeat("x", maxRequestBody)})
	if !errors.Is(err, comitia.ErrInvalid) || calls != 0 {
		t.Fatalf("request not rejected locally: calls=%d err=%v", calls, err)
	}
	// Exactly the request cap is accepted; the next encoded byte is rejected.
	err = client.call(context.Background(), "token", "messages/replay", nameRequest{Name: strings.Repeat("x", maxRequestBody-len(`{"name":""}`))}, &emptyResult{})
	if err != nil || calls != 1 {
		t.Fatalf("exact request cap: calls=%d err=%v", calls, err)
	}
}

func TestCanonicalMessageBytesBoundBothReadShapes(t *testing.T) {
	svc, client, owner, c, _ := administrationServer(t)
	ctx := context.Background()
	var messages []comitia.Message
	for _, key := range []string{"first", "second"} {
		m, err := client.AppendMessage(ctx, owner.Token, c.ID, comitia.MessageInput{DedupeKey: key, Body: "small"})
		if err != nil {
			t.Fatal(err)
		}
		messages = append(messages, m)
	}
	legacy, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	// Canonical field names are longer than the old Go names. A budget that
	// fits two old records must still bound the actual HTTP array.
	budget := len(legacy)
	for _, page := range []bool{false, true} {
		for _, wait := range []bool{false, true} {
			path := "messages/replay"
			body := fmt.Sprintf(`{"context_id":%q,"max_bytes":%d,"page":%t}`, c.ID, budget, page)
			if wait {
				path = "messages/wait"
				body = fmt.Sprintf(`{"context_id":%q,"max_bytes":%d,"page":%t,"timeout_seconds":0}`, c.ID, budget, page)
			}
			r := httptest.NewRequest("POST", "/v1/"+path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set(wireHeader, canonicalWire)
			r.Header.Set("Authorization", "Bearer "+owner.Token)
			w := httptest.NewRecorder()
			NewHandler(svc).ServeHTTP(w, r)
			var result struct {
				Messages json.RawMessage `json:"messages"`
				Page     struct {
					Messages       json.RawMessage `json:"messages"`
					NextAfter      int64           `json:"next_after"`
					ScannedThrough int64           `json:"scanned_through"`
					HasMore        bool            `json:"has_more"`
				} `json:"page"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			array := result.Messages
			if page {
				array = result.Page.Messages
			}
			if w.Code != 200 || len(array) > budget {
				t.Fatalf("page=%v wait=%v budget=%d actual=%d status=%d", page, wait, budget, len(array), w.Code)
			}
			// A trimmed page must not report scan progress past undelivered matches.
			if page && (result.Page.NextAfter != 1 || result.Page.ScannedThrough != 1 || !result.Page.HasMore) {
				t.Fatalf("page continuation: %+v", result.Page)
			}
		}
	}
}
