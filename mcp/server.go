// Server and the protocol revisions it accepts: the package's entry point.
// Transports live in stdio.go and http.go; dispatch in rpc.go; tools in tools.go.

// Package mcp exposes a deliberately small Model Context Protocol stdio server.
package mcp

import "github.com/krimvp/comitia"

const ProtocolVersion = comitia.ProtocolVersion

// SupportedProtocolVersions lists the MCP revisions this adapter accepts. It is
// deliberately just the established revision: the 2026-07-28 "modern" era adds
// required cache/result metadata to every result envelope (cacheScope, ttlMs,
// resultType) and a per-request _meta handshake. Comitia's adapter is a plain
// JSON-RPC tool server, so it advertises the revision it actually implements
// rather than claiming a framing whose extra obligations it does not meet.
// Clients that prefer the modern era fall back cleanly when they see this.
var SupportedProtocolVersions = []string{ProtocolVersion}

// ProtocolVersions is the JSON field form of SupportedProtocolVersions, used in
// the server/discover result and in rejections.
var ProtocolVersions = SupportedProtocolVersions

// SupportedProtocolVersion reports whether rev is accepted.
func SupportedProtocolVersion(rev string) bool {
	for _, v := range SupportedProtocolVersions {
		if v == rev {
			return true
		}
	}
	return false
}

// Server binds one already-scoped credential to an MCP connection. Credentials
// are process configuration and are never accepted as tool arguments.
type Server struct {
	DiagnosticHook   comitia.DiagnosticHook
	API              comitia.API
	Credential       string
	InstanceDefaults comitia.InstanceDefaults
	SessionsDir      string
	ServerURL        string
}
