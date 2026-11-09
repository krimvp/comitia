# Local daemon and remote client

This guide owns daemon transports and the REST wire contract. For installed
services use [local service](local-service.md); for harness registration use
[Connecting](CONNECTING.md). Examples below assume a private configured
database, a scoped credential and `./bin/comitia` built with
`go build -o bin/comitia ./cmd/comitia`.

Start the HTTP adapter against the same database in one terminal:

```sh
./bin/comitia serve --listen 127.0.0.1:8080
```

## REST requests

Select it from another terminal with `--server-url` or `COMITIA_SERVER_URL`, set to the base URL without `/v1`; remote calls time out after 70 seconds. Without one, commands other than `bootstrap` and `serve` need an existing `--db`. The client still supplies a scoped credential. REST requests are POSTs to `/v1/` with `Content-Type: application/json` and a bearer credential; each route strictly accepts only its own fields, including nested inputs. Canonical requests use snake_case: for example, `contexts/create` takes `{"create_context":{"workspace_id":"ID","name":"work"}}`, `messages/append` takes `{"context_id":"ID","message":{"dedupe_key":"key","body":"hello"}}`, and negotiated canonical message records return `id`, `context_id`, `session_id`, `sequence`, and `created_at`. Permission fields retain the numeric bitmask used by the Go API; grant expiry is an RFC3339 `expires_at` timestamp.

Session minting takes `ttl_seconds`; delegation takes `delegate: {principal_name, ttl_seconds, grants: [{context_id, permissions, expires_at}]}`. Task claim/renew take `lease_seconds` (default 300), or `open_ended: true`. Seconds may be fractional; leases must be 1–86400 seconds and session TTLs at least 1. Omitted list limits default to 100; explicit limits must be 1–1000. An explicit zero lease or limit is invalid. Renew/complete/release require `lease_version`, including an explicit zero when applicable. Raw HTTP does not apply caller environment defaults.

## REST wire compatibility

The `/v1/` compatibility policy supports either client/server upgrade order. Without negotiation, responses keep the original Go record names (`ID`, `WorkspaceID`, `LeaseVersion`, `Token`, etc.), while already-tagged envelope, instance and page fields retain their existing snake_case names. Send `X-Comitia-Wire: canonical` to explicitly request all-snake_case record responses; the new Handler confirms that representation with the same response header. Existing raw callers can keep their original field lookups. Old Handlers ignore the header and continue returning legacy records.

The new Go Client always sends the old-compatible `/v1/` request representation: nested Go record names and exact nanosecond `ttl`/`TTL` values, including open leases. It requests canonical responses, accepts both response spellings, and tolerates additive fields. This needs no discovery request, version cache or write retry. Optional members a server may predate are sent only when used: task `operation_id` (create: `task.operation_id`; claim/renew: top level) and message `task_fence`. Old Handlers reject them as unknown fields before applying the request, and the Client reports `ErrTaskMutationReceiptsUnsupported` or `ErrTaskResultFencingUnsupported` rather than retrying without idempotency or the fence. It requires collection members when the server confirms canonical responses; for old unnegotiated servers, omitted/null empty list members are normalized to `[]`. The new Handler always emits required arrays in either format, and the inspector accepts both record generations.

The new Handler accepts both canonical and legacy request forms through the same strict per-endpoint DTOs. Mixing a legacy alias with its canonical field, even with the same value or null, is invalid; `ttl` excludes `ttl_seconds`/`lease_seconds`, and `open_ended: true` excludes duration fields. Go library, CLI and MCP record names remain unchanged. List responses always include their route's `contexts`, `participants`, `tasks`, `messages`, or `instances` array, including `[]` when empty; message page mode instead returns `page: {messages, next_after, scanned_through, has_more}`, with the same page field names in legacy and canonical responses; a Client reading a server that predates `scanned_through` treats it as `next_after`. With `page: true` or a nonempty `after_id`, `tasks/list` returns `task_page: {tasks, next_after, has_more}` and `participants/list` returns `participant_page: {participants, next_after, has_more}`; page keys are the same in both representations and only the records use the negotiated spelling. A new Client paging against a Handler without discovery paging falls back to the bounded legacy list, which fails explicitly at the 1000-entry ceiling instead of hiding a tail. Message replay and wait accept the opt-in `strict_recipients` member, and `messages/schemas/list` / `messages/schemas/get` return `message_schemas` / `message_schema` descriptors with the same snake_case names in both representations. A new Client reports both capabilities as explicitly unsupported by an older server rather than widening a strict read or reporting an unknown schema. MCP and CLI also return `[]` for empty collections.

Select the daemon and credential when running the CLI:

```sh
COMITIA_SERVER_URL=http://127.0.0.1:8080 \
COMITIA_CREDENTIAL="$agent_b_token" \
  ./bin/comitia replay --context-id "$context_id" --after 0 --limit 100
```

The MCP adapter also uses embedded SQLite by default and binds one credential to its stdio connection:

```sh
COMITIA_CREDENTIAL="$agent_b_token" ./bin/comitia mcp
```

## Delegated helpers

Use the [installed spawn flow](local-service.md#spawn-flow-a-delegated-session-per-task-owner)
for permissions, private credential files, Codex/Claude launchers, revocation and pruning.
For embedded MCP, configure `mcp --credential-server-url URL` to a daemon serving
the same database; otherwise delegation returns `ErrInvalid`. Remote stdio MCP
uses `--server-url` / `COMITIA_SERVER_URL` to populate delegated files.
A returned path belongs to the MCP host; delegation does not launch an agent.

Stdio offers `delegate_session`. HTTP MCP hides and refuses it unless
`--allow-delegation` is enabled, because a remote client cannot read server-local
files. See [credential-file safeguards and HTTP options](security.md#credential-proof).

Both transports support bounded long polling with `wait_messages` (default 30
seconds, capped at 60), with no SSE or server push. On macOS, use MCP
`wait_messages`, wrapper `watch` or replay: `comitia-local` rejects CLI `wait`
as a legacy command. See [message reads](message-protocol.md#filtered-replay-and-waiting).
