package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/krimvp/comitia/pkg/comitia"
)

func TestPresenceResponseUpgradeOrders(t *testing.T) {
	// Frozen old response: new clients must preserve absent recency as unknown.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if len(input) != 1 || input["context_id"] != "context" {
			t.Errorf("changed list request: %v", input)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"instances":[{"id":"worker","session_id":"session","context_id":"context","last_seen":"2026-10-05T12:00:00Z","active":true}]}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	rows, err := client.ListInstances(context.Background(), "credential", "context")
	if err != nil || len(rows) != 1 || rows[0].SeenSecondsAgo != nil || rows[0].Recent != nil || !rows[0].Active {
		t.Fatalf("new client/old server: %v %v", rows, err)
	}
	// Frozen old consumer schema ignores new fields, including false and zero.
	seconds, recent := int64(0), false
	rows[0].SeenSecondsAgo, rows[0].Recent = &seconds, &recent
	encoded, err := json.Marshal(instancesResult{rows})
	if err != nil {
		t.Fatal(err)
	}
	var old struct {
		Instances []struct {
			ID       string    `json:"id"`
			Active   bool      `json:"active"`
			LastSeen time.Time `json:"last_seen"`
		} `json:"instances"`
	}
	if err := json.Unmarshal(encoded, &old); err != nil || len(old.Instances) != 1 || old.Instances[0].ID != "worker" || !old.Instances[0].Active || old.Instances[0].LastSeen.IsZero() {
		t.Fatalf("old client/new server: %v %v", old, err)
	}
	var current instancesResult
	if err := json.Unmarshal(encoded, &current); err != nil || current.Instances[0].SeenSecondsAgo == nil || *current.Instances[0].SeenSecondsAgo != 0 || current.Instances[0].Recent == nil || *current.Instances[0].Recent {
		t.Fatalf("zero/false fields omitted: %v %v", current, err)
	}
	empty, _ := json.Marshal(comitia.InstancePresence{})
	var fields map[string]any
	_ = json.Unmarshal(empty, &fields)
	if _, ok := fields["seen_seconds_ago"]; ok {
		t.Fatal("unset age should be omitted")
	}
	if _, ok := fields["recent"]; ok {
		t.Fatal("unset recency should be omitted")
	}
}
