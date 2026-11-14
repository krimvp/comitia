// Package comitia is the public API of Comitia, a coordination service for
// agents: a durable message log, work contexts, and a task board with leases.
// Its import path is github.com/krimvp/comitia/pkg/comitia.
//
// This package holds only the API: aliases of the types, forwarders to the
// functions and the build stamp variables. Its files read as a table of
// contents: api.go (the API, its configuration and New), errors.go, sessions.go,
// contexts.go, messages.go, tasks.go, presence.go, server.go and store.go.
//
// The implementation lives in internal packages, each importing only those
// listed after it:
//
//	internal/model       data types, errors, permissions, API and Store interfaces, shared rules
//	internal/schema      message schema registry, payload types and validators: model
//	internal/sqlstore    the SQL store and its migrations: model
//	internal/service     the Service and all its methods: model, schema
//	internal/mergegate   merge certificate verification: model, schema
//	internal/evaluation  evaluation reports: model, schema
//	internal/httpserve   HTTP request logging and the serve-and-shut-down loop: nothing
//
// No internal package imports this one.
//
// The other packages of the module sit beside this one under pkg/, with the
// command under cmd/:
//
//	cmd/comitia     the CLI, the HTTP server and the MCP servers
//	pkg/httpapi     the HTTP adapter: handler, client and the inspector UI
//	pkg/mcp         the MCP adapter, over stdio and HTTP
//	pkg/sqlite      registers the SQLite driver and opens embedded stores
//	pkg/storetest   conformance checks for Store implementations
//
// The adapters cmd/comitia, pkg/mcp and pkg/httpapi use only this package and
// pkg/sqlite, except that cmd/comitia and pkg/mcp share their HTTP listener
// code through internal/httpserve. Remote clients that import this package,
// pkg/httpapi or pkg/mcp do not link the SQLite driver; only pkg/sqlite does.
package comitia
