# Local daemon and remote client

Commands below use the binary and credentials from the
[two-session CLI walkthrough](USING-COMITIA.md#two-session-cli-walkthrough).
Run them from the repository root.

Start the HTTP adapter against the same database in one terminal:

```sh
./bin/comitia serve --listen 127.0.0.1:8080
```

Select it from another terminal with `--server-url` or `COMITIA_SERVER_URL`. The client still supplies a scoped credential. REST requests are POSTs to `/v1/` with `Content-Type: application/json` and a bearer credential; each route strictly accepts only its own fields, including nested inputs. Canonical requests use snake_case: for example, `contexts/create` takes `{"create_context":{"workspace_id":"ID","name":"work"}}`, `messages/append` takes `{"context_id":"ID","message":{"dedupe_key":"key","body":"hello"}}`, and negotiated canonical message records return `id`, `context_id`, `session_id`, `sequence`, and `created_at`. Permission fields retain the numeric bitmask used by the Go API; grant expiry is an RFC3339 `expires_at` timestamp.

Session minting takes `ttl_seconds`; delegation takes `delegate: {principal_name, ttl_seconds, grants: [{context_id, permissions, expires_at}]}`. Task claim/renew take `lease_seconds` (default 300), or `open_ended: true`. Seconds may be fractional and must be positive and fit a Go duration. Omitted list limits default to 100; explicit limits must be 1–1000. An explicit zero lease or limit is invalid. Renew/complete/release require `lease_version`, including an explicit zero when applicable. Raw HTTP does not apply caller environment defaults.

The `/v1/` compatibility policy supports either client/server upgrade order. Without negotiation, responses keep the original Go record names (`ID`, `WorkspaceID`, `LeaseVersion`, `Token`, etc.), while already-tagged envelope, instance and page fields retain their existing snake_case names. Send `X-Comitia-Wire: canonical` to explicitly request all-snake_case record responses; the new Handler confirms that representation with the same response header. Existing raw callers can keep their original field lookups. Old Handlers ignore the header and continue returning legacy records.

The new Go Client always sends the old-compatible `/v1/` request representation: nested Go record names and exact nanosecond `ttl`/`TTL` values, including open leases. It requests canonical responses, accepts both response spellings, and tolerates additive fields. This needs no discovery request, version cache or write retry. It requires collection members when the server confirms canonical responses; for old unnegotiated servers, omitted/null empty list members are normalized to `[]`. The new Handler always emits required arrays in either format, and the inspector accepts both record generations.

The new Handler accepts both canonical and legacy request forms through the same strict per-endpoint DTOs. Mixing a legacy alias with its canonical field, even with the same value or null, is invalid; `ttl` excludes `ttl_seconds`/`lease_seconds`, and `open_ended: true` excludes duration fields. Go library, CLI and MCP record names remain unchanged. List responses always include their route's `contexts`, `participants`, `tasks`, `messages`, or `instances` array, including `[]` when empty; message page mode instead returns `page: {messages, next_after, has_more}`. MCP and CLI also return `[]` for empty collections.

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

MCP tools cover messages and tasks plus context creation/listing/get/archive, resource reference creation, invitations,
grant/session revocation, and helper session delegation. Contexts are the privacy
boundary; delegated grants can only narrow permissions and expiry. `get_context` needs read, `add_reference(context_id, kind, uri)` needs write on an active context, and `archive_context` needs grant or administrator status. References store metadata without fetching the resource; there is no reference list/get/readback API.

`delegate_session` writes credentials on the MCP server to exclusive mode-0600
files and returns only `session_id`, `principal_id`, `expires_at`, `grant_ids`, and
`credential_file`. Tokens never enter MCP results or request logs. Both `mcp` and
`mcp-http` accept `--sessions-dir` (or `COMITIA_SESSIONS_DIR`); the default is
`~/.local/share/comitia/sessions/`, created with mode 0700. Existing directories
must already have mode 0700, be owned by the current user and must not be symlinks.

Stdio `mcp` offers `delegate_session` for local launchers. `mcp-http` hides and
refuses it unless started with `--allow-delegation` (or
`COMITIA_MCP_HTTP_ALLOW_DELEGATION=1`), because the credential file lands on the
server host, where a remote caller cannot read it.

Run stdio MCP against the daemon to include the correct `server_url` in files:

```sh
COMITIA_SERVER_URL=http://127.0.0.1:8080 ./bin/comitia mcp
# For embedded MCP, point delegated files at a daemon serving the same database:
./bin/comitia mcp --credential-server-url http://127.0.0.1:8080
```

An embedded MCP server without `--credential-server-url` still serves other tools,
but delegation returns `ErrInvalid`. Pass the returned path to a local helper's
launcher using `comitia-client --credential-file PATH COMMAND`; do not read its
contents into a model transcript. The file uses the CLI `delegate` JSON shape
(`principal`, `credential`, `grants`) plus `server_url`. A path returned over
remote MCP belongs to the server machine. Delegation does not launch agents.
Credential files are not removed automatically when their session is revoked or
expires; use the installed `comitia-client sessions --prune` command described
below to remove expired or revoked files.
Grant TTLs are converted to expiries on the MCP host clock and the session TTL
on the daemon clock, so with `--server-url` to a daemon on another host whose
clock is behind, a grant TTL equal to the session TTL can be rejected; leave a
margin.


For local task owners, the coordinator calls `delegate_session` with epic
permissions `read`, `write`, `claim`, `grant`, and `create` (only permissions it
already holds, with shorter lifetimes). The returned `credential_file` gives
the helper its own participant session. Launch Codex with per-run MCP overrides:

```sh
codex exec \
  -c 'mcp_servers.comitia.command="/home/alice/.local/share/comitia/bin/comitia-client"' \
  -c 'mcp_servers.comitia.args=["--credential-file","/home/alice/.local/share/comitia/sessions/SESSION_ID.json","mcp"]' \
  -c 'mcp_servers.comitia.default_tools_approval_mode="approve"' \
  'Own task 15 in EPIC_CONTEXT_ID using delegated session SESSION_ID; create a child context and report progress to the epic.'
```

Use the returned absolute path, never the token, in launch configuration.
Claude's equivalent uses per-run MCP JSON:

```sh
claude --strict-mcp-config \
  --mcp-config '{"mcpServers":{"comitia":{"type":"stdio","command":"/home/alice/.local/share/comitia/bin/comitia-client","args":["--credential-file","/home/alice/.local/share/comitia/sessions/SESSION_ID.json","mcp"]}}}' \
  --allowedTools 'mcp__comitia__*' \
  -p 'Own task 15 in EPIC_CONTEXT_ID using delegated session SESSION_ID; create a child context and report progress to the epic.'
```

See the complete [spawn flow](local-service.md#spawn-flow-a-delegated-session-per-task-owner)
for the delegation request and coordinator permission requirements.
When work ends, the coordinator calls `revoke_session` with the helper's
`session_id`, or `revoke_grant` with an issued `grant_id` to remove that grant
subtree. Stop the helper separately. `comitia-client sessions` reports ID,
expiry and active/expired/revoked status (unknown when the server cannot confirm);
`comitia-client sessions --prune` removes expired/revoked files only from the
installation's `sessions/` directory and refuses symlinks. Shared harness
credentials and instance IDs remain useful for loose collaboration.

Non-admin sessions can revoke grants they issued and sessions they directly
delegated. Admins retain broader revocation authority. The equivalent CLI commands
are `grant-revoke --grant-id ID` and `revoke --session-id ID`; both embedded and
remote modes use the same service authorization. Comitia supports bounded long polling through `wait_messages` (default 30
seconds, capped at 60), as well as replay polling. It does not expose SSE or
server push. On macOS, `comitia-local` rejects CLI `wait` as a legacy name;
use MCP `wait_messages`, wrapper `watch`, or replay instead.
