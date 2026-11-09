package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/krimvp/comitia"
)

// REST shares the core lease and TTL bounds instead of minting expired leases
// or sessions; legacy nanosecond ttl fields still decode.
func TestRESTRejectsOutOfBoundsLeasesAndTTLs(t *testing.T) {
	svc, client, owner, c, _ := administrationServer(t)
	server := httptest.NewServer(NewHandler(svc))
	t.Cleanup(server.Close)
	task, err := client.CreateTask(context.Background(), owner.Token, c.ID, comitia.TaskInput{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := client.CreatePrincipal(context.Background(), owner.Token, "p")
	if err != nil {
		t.Fatal(err)
	}
	post := func(route, body string) (int, string) {
		req, _ := http.NewRequest(http.MethodPost, server.URL+"/v1/"+route, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+owner.Token)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(raw)
	}
	id := `"task_id":"` + task.ID + `"`
	for _, tc := range []struct{ route, body, want string }{
		{"tasks/claim", `{` + id + `,"lease_seconds":0.000001}`, "between 1s and 24h"},
		{"tasks/claim", `{` + id + `,"ttl":1}`, "between 1s and 24h"},
		{"tasks/claim", `{` + id + `,"lease_seconds":9000000000}`, "between 1s and 24h"},
		{"sessions/mint", `{"principal_id":"` + p.ID + `","ttl":1}`, "at least 1s"},
		{"sessions/mint", `{"principal_id":"` + p.ID + `","ttl_seconds":0.5}`, "at least 1s"},
	} {
		if status, body := post(tc.route, tc.body); status != http.StatusBadRequest || !strings.Contains(body, tc.want) {
			t.Fatalf("%s %s: %d %s", tc.route, tc.body, status, body)
		}
	}
	if status, body := post("tasks/claim", `{`+id+`,"ttl":1500000000}`); status != http.StatusOK {
		t.Fatalf("legacy ttl claim: %d %s", status, body)
	}
}
