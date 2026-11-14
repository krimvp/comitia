# Code map

How to find things in the code. [Architecture](architecture.md#application-boundaries)
states the boundaries and which package may import which; this page says where
each part lives. Paths are relative to the repository root.

## Where each domain lives

Files are named by domain in every layer, so one prefix finds a domain
everywhere. The root file is the public facade (aliases and forwarders only).

| Domain | Root | `internal/model` | `internal/service` | `internal/sqlstore` |
|---|---|---|---|---|
| Sessions and principals | `sessions.go` | `sessions.go` | `sessions.go`, `service.go` (authentication) | `sessions.go` |
| Grants | `sessions.go`, `store.go` | `grant_policy.go`, `permissions.go` | `grants*.go` | `grants.go`, `grant_policy.go` |
| Contexts and references | `contexts.go` | `contexts.go` | `contexts*.go` | `contexts.go`, `context_*.go` |
| Messages | `messages.go` | `messages.go`, `schema_ids.go` | `messages_*.go`, `pages_messages.go` | `messages.go`, `message_capabilities.go` |
| Tasks | `tasks.go` | `tasks.go` | `tasks*.go` | `tasks.go` |
| Presence | `presence.go` | `presence.go` | `presence.go` | `presence.go` |
| Server info and storage status | `server.go` | `server_info.go` | `serverinfo*.go` | none |

Elsewhere:

- **Message schemas:** `internal/schema/registry.go` holds the registry (each
  schema ID with its validator and descriptor); the payload types and rules are in
  the other files of `internal/schema`. Schema ID constants are in
  `internal/model/schema_ids.go`, re-exported by the root `messages.go`.
- **Migrations:** `internal/sqlstore/migrations.go` (schema generations,
  backfills, repair queues). Each domain's DDL is a constant in its own
  `internal/sqlstore` file, listed in `schemas`.
- **Errors and limits:** the sentinel errors are in `internal/model/api.go`;
  error classes, operation names and `SafeError` in `internal/model/operations.go`;
  each limit beside its domain (`messages.go`, `tasks.go`, `pages.go`). The root
  `errors.go` exports them.
- **Merge gates and evaluation reports:** `internal/mergegate`,
  `internal/evaluation`.
- **Adapters:** `httpapi/server_<domain>.go` and `httpapi/client_<domain>.go`
  with the wire types in `httpapi/wire*.go`; `mcp/tools_<domain>.go` with the
  registry in `mcp/tools.go`; `cmd/comitia/cmd_<domain>.go` with the usage text in
  `cmd/comitia/usage.go`.

## A request through the code

`comitia send` against a server, followed from the command line to SQLite and back:

1. `run` (`cmd/comitia/main.go`) parses the global flags and calls `dispatch`
   once with `parseOnly` set, so flag errors come before storage. `open`
   (`open.go`) then returns an `*httpapi.Client` when a server URL is set, or a
   `*comitia.Service` over `sqlite.OpenSQLite`.
2. `dispatch` tries each of `commandGroups`; `runMessages` calls `runSend`
   (`cmd_messages.go`), which builds a `comitia.MessageInput` and calls
   `AppendMessage` on the API.
3. `(*Client).AppendMessage` (`httpapi/client_messages.go`) calls
   `(*Client).call` (`client.go`), which posts to `/v1/messages/append`.
4. On the server, `(*handler).ServeHTTP` (`httpapi/server.go`) checks the
   method, bearer credential and media type and looks the route up in the table
   that `endpoints` builds from each domain's routes (`messageRoutes` in
   `server_messages.go`). `typedEndpoint` decodes the body and calls
   `AppendMessage` on the server's `*Service`.
5. `(*Service).AppendMessage` (`internal/service/messages_append.go`) validates
   the input and the envelope (`normalizeMessageEnvelope`, against the schema
   registry), then runs one `Update` transaction: `require` (`service.go`)
   authenticates and checks the grant, `MessageByDedupe` finds a retry, and
   `PutMessage` stores the message.
6. `(*SQLiteStore).Update` (`internal/sqlstore/store.go`) admits one writer,
   begins the transaction, checks the schema version and commits when the
   callback returns nil. `(*sqlStore).PutMessage` (`messages.go`) assigns the
   sequence and writes the rows.
7. Back out, the handler encodes a `messageResult` (`httpapi/wire.go`),
   in legacy field names unless the client negotiated the canonical wire
   (`legacyValue`, `wire_compat.go`). A failure goes through `SafeError` and
   `writeOperationError` (`httpapi/errors.go`); the client rebuilds it with
   `remoteCause`, and the CLI maps it to an exit status in `exitCode`
   (`cmd/comitia/exit.go`).

Over MCP the same call enters at `(*Server).handle` (`mcp/rpc.go`), which passes
`tools/call` to `(*Server).call` (`mcp/tools.go`); `callMessages` and
`callSendMessage` (`mcp/tools_messages.go`) call `AppendMessage` on the API.

## Where the tests are

- **`internal/service`** holds most tests. They exercise the domain rules
  through `*Service` on a real SQLite store (the `setup` fixture in
  `setup_test.go`), the layer every adapter calls, and some reach unexported
  state such as the schema registry. They moved there with the service; tests of
  internal packages may not import the root.
- **`internal/sqlstore`**: migrations, backfills, WAL, file permissions and
  concurrency of the store alone. **`internal/schema`**, **`internal/model`**,
  **`internal/evaluation`**, **`internal/httpserve`**: their own units.
- **Root**: the structure guards (`surface_test.go`, `layering_test.go`,
  `import_boundary_test.go`), the limit values, store conformance through
  `storetest`, and tests that need only the public API.
- **`httpapi`**: wire format, error bodies and upgrade-order compatibility
  against the frozen client and handler in `httpapi/internal/legacy`.
  **`mcp`**: tool schemas (`schema_conformance_test.go`), stdio and HTTP
  transports. **`cmd/comitia`**: commands run as processes, flags, help and exit
  codes.
- **`scripts/test_*.py`**: documentation contracts, installers and tooling.
  **`httpapi/ui-tests`**, **`site`**: the inspector and landing page in Node and
  in a browser.
