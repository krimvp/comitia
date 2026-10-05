package comitia

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestServerInfoSessionAndFeatures(t *testing.T) {
	f := setup(t, ":memory:")
	defer f.store.Close()
	ctx := context.Background()
	// A session without any context grant can still discover capabilities.
	p, err := f.svc.CreatePrincipal(ctx, f.admin.Token, "unscoped")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.svc.MintSession(ctx, f.admin.Token, p.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.svc.ServerInfo(ctx, c.Token)
	if err != nil || info.Version == "" || info.ProtocolVersion != ProtocolVersion {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	want := []string{"agent_instances", "default_agent_instance", "child_contexts", "invites", "context_archive", "task_status", "approvals", "instance_claims", "replay_filters", "wait_messages", "context_tools", "grant_tools", "delegated_sessions", "presence"}
	for _, feature := range want {
		if !slices.Contains(info.Features, feature) {
			t.Fatalf("missing feature %q: %v", feature, info.Features)
		}
	}
	want = append([]string{}, info.Features...)
	info.Features[0] = "mutated"
	next, _ := f.svc.ServerInfo(ctx, c.Token)
	if !reflect.DeepEqual(next.Features, want) {
		t.Fatal("caller changed the feature table")
	}
	if err := f.svc.RevokeSession(ctx, f.admin.Token, c.Session.ID); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"", "invalid-secret", c.Token} {
		if _, err := f.svc.ServerInfo(ctx, token); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("authentication: %v", err)
		}
	}
	expiring, err := f.svc.MintSession(ctx, f.admin.Token, p.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	*f.now = f.now.Add(2 * time.Minute)
	if _, err := f.svc.ServerInfo(ctx, expiring.Token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired session: %v", err)
	}
}

func TestBuildVersion(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = ""
	if got := BuildVersion(); got != "dev" {
		t.Fatalf("fallback=%q", got)
	}
	Version = "v1.2.3"
	if got := BuildVersion(); got != "v1.2.3" {
		t.Fatalf("ldflags override=%q", got)
	}
}

func TestServerInfoWithClient(t *testing.T) {
	server := ServerInfo{Features: Features(), Build: BuildInfo{Version: "dev", Revision: "abc", Dirty: "false"}}
	tests := []struct {
		name    string
		client  BuildInfo
		extra   []string
		stale   bool
		missing []string
		hint    string
	}{
		{"same build", BuildInfo{Version: "dev", Revision: "abc", Dirty: "false"}, nil, false, nil, ""},
		{"unknown client revision", BuildInfo{Version: "dev", Revision: "unknown", Dirty: "unknown"}, nil, false, nil, ""},
		{"older client revision", BuildInfo{Version: "dev", Revision: "old", Dirty: "false"}, nil, true, nil, "different builds"},
		{"release client, dev server", BuildInfo{Version: "v1.0.0", Revision: "unknown"}, nil, false, nil, ""},
		{"server features unknown to client", BuildInfo{Version: "dev", Revision: "unknown"}, []string{"future_feature"}, true, []string{"future_feature"}, "restart the MCP client"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := server
			in.Features = append(Features(), tc.extra...)
			got := in.WithClient(tc.client)
			if got.Client == nil || *got.Client != tc.client || got.ClientStale == nil || *got.ClientStale != tc.stale || !reflect.DeepEqual(got.ClientMissingFeatures, tc.missing) {
				t.Fatalf("got=%+v", got)
			}
			if (tc.hint == "") != (got.ClientHint == "") || !strings.Contains(got.ClientHint, tc.hint) {
				t.Fatalf("hint=%q", got.ClientHint)
			}
		})
	}
	released := ServerInfo{Features: Features(), Build: BuildInfo{Version: "v1.1.0", Revision: "unknown"}}
	if got := released.WithClient(BuildInfo{Version: "v1.0.0", Revision: "unknown"}); !*got.ClientStale {
		t.Fatalf("release mismatch=%+v", got)
	}
	// Service results never carry client fields; adapters add them.
	f := setup(t, ":memory:")
	defer f.store.Close()
	info, err := f.svc.ServerInfo(context.Background(), f.admin.Token)
	if err != nil || info.Client != nil || info.ClientStale != nil || info.ClientHint != "" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	raw, _ := json.Marshal(info)
	if strings.Contains(string(raw), "client") {
		t.Fatalf("wire=%s", raw)
	}
}
