package httpapi

import (
	"context"
	"github.com/krimvp/comitia"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type emptyCollectionsAPI struct{ comitia.API }

func (emptyCollectionsAPI) ListContexts(context.Context, string, string, int) ([]comitia.WorkContext, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ListChildContexts(context.Context, string, string, string, int) ([]comitia.WorkContext, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ListParticipants(context.Context, string, string, int) ([]comitia.Participant, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ListTasks(context.Context, string, string, comitia.TaskStatus, int) ([]comitia.Task, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ListInstances(context.Context, string, string) ([]comitia.InstancePresence, error) {
	return nil, nil
}
func (emptyCollectionsAPI) ReplayMessages(context.Context, string, string, int64, int, ...comitia.MessageFilters) ([]comitia.Message, error) {
	return nil, nil
}
func (emptyCollectionsAPI) WaitMessages(context.Context, string, string, int64, time.Duration, ...comitia.MessageFilters) ([]comitia.Message, error) {
	return nil, nil
}

func TestEveryHTTPEmptyCollectionHasRequiredArray(t *testing.T) {
	h := NewHandler(emptyCollectionsAPI{})
	for _, tc := range []struct{ path, body, member string }{{"contexts/list", `{}`, "contexts"}, {"contexts/children", `{"context_id":"c"}`, "contexts"}, {"participants/list", `{"context_id":"c"}`, "participants"}, {"tasks/list", `{"context_id":"c"}`, "tasks"}, {"instances/list", `{"context_id":"c"}`, "instances"}, {"messages/replay", `{"context_id":"c"}`, "messages"}, {"messages/wait", `{"context_id":"c","timeout_seconds":0}`, "messages"}} {
		t.Run(tc.path, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/v1/"+tc.path, strings.NewReader(tc.body))
			r.Header.Set("Authorization", "Bearer caller")
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"`+tc.member+`":[]}` {
				t.Fatalf("empty: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestHTTPClientRejectsMissingCollectionMembers(t *testing.T) {
	for _, body := range []string{`{}`, `{"tasks":null}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) }))
		client, err := NewClientWithInstanceDefaults(server.URL, server.Client(), comitia.InstanceDefaults{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.ListTasks(context.Background(), "caller", "c", "", 100); err == nil {
			t.Fatalf("accepted missing collection: %s", body)
		}
		server.Close()
	}
}
