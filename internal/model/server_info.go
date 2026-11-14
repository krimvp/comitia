package model

import (
	"context"
	"runtime/debug"
)

const ProtocolVersion = "2025-06-18"

// BuildStamp returns the current build stamp: the exported variables Version,
// Revision and Dirty of pkg/comitia, which may be set with -ldflags -X or
// assigned at runtime. The init of pkg/comitia installs a reader of those
// variables, so every call sees their current values; model cannot import
// pkg/comitia. Until then, and for tests that replace it, the stamp is empty
// and the defaults below apply. Read it only through BuildVersion and
// BuildIdentity, never from a package-level initialiser, which may run before
// the init of pkg/comitia.
var BuildStamp = func() (version, revision, dirty string) { return "", "", "" }

type BuildInfo struct {
	Version  string `json:"version"`
	Revision string `json:"revision"`
	Dirty    string `json:"dirty"`
}

func BuildIdentity() BuildInfo {
	info, _ := debug.ReadBuildInfo()
	return IdentityFromBuildInfo(info)
}

func IdentityFromBuildInfo(info *debug.BuildInfo) BuildInfo {
	_, revision, dirty := BuildStamp()
	b := BuildInfo{Version: BuildVersion(), Revision: revision, Dirty: dirty}
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
	if version, _, _ := BuildStamp(); version != "" {
		return version
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
	// Limits lists content write limits; older servers omit it.
	Limits *ContentLimits `json:"limits,omitempty"`

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
	"content_limits",
	"context_references",
}
