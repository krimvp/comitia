// Package httpserve holds what the HTTP listeners of cmd/comitia (serve) and
// mcp (the remote MCP transport) share: the request-logging wrapper with its
// status recorder and method allowlist, and the serve-and-shut-down loop. Each
// caller keeps its own log line, timeouts and shutdown grace. It imports no
// other package of the module.
package httpserve
