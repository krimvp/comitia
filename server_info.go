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
	"task_result_fencing",
	"replay_filters",
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
	return ServerInfo{Build: BuildIdentity(), Version: BuildVersion(), ProtocolVersion: ProtocolVersion, Features: append([]string{}, serverFeatures...)}, nil
}
