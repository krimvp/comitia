package comitia

import (
	"context"
	"runtime/debug"
)

const ProtocolVersion = "2025-06-18"

// Version can be set with -ldflags '-X github.com/krimvp/comitia.Version=...'.
var Version string

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
	"replay_filters",
	"wait_messages",
	"context_tools",
	"grant_tools",
	"delegated_sessions",
	"presence",
}

func (s *Service) ServerInfo(ctx context.Context, raw string) (ServerInfo, error) {
	err := s.store.View(ctx, func(r ReadStore) error {
		_, err := s.auth(r, raw)
		return err
	})
	if err != nil {
		return ServerInfo{}, err
	}
	return ServerInfo{Version: BuildVersion(), ProtocolVersion: ProtocolVersion, Features: append([]string{}, serverFeatures...)}, nil
}
