package comitia

import (
	"context"
	"errors"
	"reflect"
	"slices"
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
	want := []string{"agent_instances", "default_agent_instance", "child_contexts", "invites", "context_archive", "task_status", "approvals", "instance_claims", "replay_filters", "wait_messages", "context_tools", "grant_tools", "delegated_sessions"}
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
