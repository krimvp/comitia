// Package comitia is the public API of Comitia, a coordination service for
// agents: a durable message log, work contexts, and a task board with leases.
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
// The adapters cmd/comitia, mcp and httpapi use only this package and sqlite,
// except that cmd/comitia and mcp share their HTTP listener code through
// internal/httpserve.
// Package sqlite registers the SQLite driver and opens embedded stores, so
// remote clients that import comitia, httpapi or mcp do not link the driver.
// Package storetest checks Store implementations for conformance.
package comitia
