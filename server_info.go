package comitia

import (
	"context"
	"runtime/debug"
)

const ProtocolVersion = "2025-06-18"

// Version can be set with -ldflags '-X github.com/krimvp/comitia.Version=...'.
var Version string

// Revision and Dirty may be stamped by source installers even with buildvcs=false.
var Revision, Dirty string

type BuildInfo struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
	Dirty    string `json:"dirty"`
}

func BuildIdentity() BuildInfo {
	info, _ := debug.ReadBuildInfo()
	return buildIdentity(info)
}

func buildIdentity(info *debug.BuildInfo) BuildInfo {
	b := BuildInfo{Version: BuildVersion(), Revision: Revision, Dirty: Dirty}
	if info != nil {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && b.Revision == "" {
				b.Revision = setting.Value
			}
			if setting.Key == "vcs.modified" && b.Dirty == "" {
				b.Dirty = setting.Value
			}
		}
	}
	if b.Revision == "" {
		b.Revision = "unknown"
	}
	if b.Dirty != "true" && b.Dirty != "false" {
		b.Dirty = "unknown"
	}
	return b
}

func BuildVersion() string {
	if Version != "" {
		return Version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}

type ServerInfo struct {
	Version         string   `json:"version"`
	ProtocolVersion string   `json:"protocol_version"`
	Features        []string `json:"features"`

	Build BuildInfo `json:"build"`

	// Client fields are set only by adapters that report their own build, such
	// as the MCP server_info tool; see WithClient.
	Client                *BuildInfo `json:"client,omitempty"`
	ClientStale           *bool      `json:"client_stale,omitempty"`
	ClientMissingFeatures []string   `json:"client_missing_features,omitempty"`
	ClientHint            string     `json:"client_hint,omitempty"`
}

// Features returns the feature inventory compiled into this binary.
func Features() []string { return append([]string{}, serverFeatures...) }

// WithClient reports how a client built as client relates to the server. The
// client is stale when the server advertises features this build does not
// know, or when both sides report a known, different revision; release
// version labels are compared only when either revision is unknown.
func (info ServerInfo) WithClient(client BuildInfo) ServerInfo {
	info.Client = &client
	known := make(map[string]bool, len(serverFeatures))
	for _, f := range serverFeatures {
		known[f] = true
	}
	info.ClientMissingFeatures = nil
	for _, f := range info.Features {
		if !known[f] {
			info.ClientMissingFeatures = append(info.ClientMissingFeatures, f)
		}
	}
	// Builds of one commit may carry different version labels, so a label
	// counts only when either revision is unknown.
	set := func(v, unknown string) bool { return v != "" && v != unknown }
	builds := client.Revision != info.Build.Revision
	if !set(client.Revision, "unknown") || !set(info.Build.Revision, "unknown") {
		builds = set(client.Version, "dev") && set(info.Build.Version, "dev") && client.Version != info.Build.Version
	}
	stale := len(info.ClientMissingFeatures) > 0 || builds
	info.ClientStale = &stale
	info.ClientHint = ""
	switch {
	case len(info.ClientMissingFeatures) > 0:
		info.ClientHint = "the server is newer than this MCP client; restart the MCP client (the agent session) to load the upgraded tool schemas"
	case stale:
		info.ClientHint = "this MCP client and the server run different builds; restart the MCP client (the agent session) after upgrading"
	}
	return info
}

type ServerInfoAPI interface {
	ServerInfo(context.Context, string) (ServerInfo, error)
}

// Add features here only when their implementation has landed. Features describe
// server capabilities; a running MCP adapter may still have an older schema.
var serverFeatures = []string{
	"agent_instances",
	"default_agent_instance",
	"child_contexts",
	"invites",
	"context_archive",
	"task_status",
	"approvals",
	"instance_claims",
	"task_mutation_receipts",
	"task_claim_recovery",
	"task_result_fencing",
	"replay_filters",
	"strict_recipients",
	"message_schema_discovery",
	"scoped_message_queries",
	"durable_requests",
	"merge_certificates",
	"wait_messages",
	"context_tools",
	"grant_tools",
	"delegated_sessions",
	"presence",
	"storage_status",
	"operational_errors",
	"build_provenance",
}

func (s *Service) ServerInfo(ctx context.Context, raw string) (ServerInfo, error) {
	err := s.store.View(ctx, func(r ReadStore) error {
		_, err := s.auth(r, raw)
		return err
	})
	if err != nil {
		return ServerInfo{}, err
	}
	return ServerInfo{Build: BuildIdentity(), Version: BuildVersion(), ProtocolVersion: ProtocolVersion, Features: Features()}, nil
}
