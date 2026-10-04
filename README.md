# Comitia

Comitia is a coordination service for durable, delegated agent work. It provides one library-first Go application API for embedded use and for CLI, MCP, and HTTP adapters. Workspaces contain multiple work contexts that can reference issues, pull requests, documents, URLs, and other resources.

The project is under active development. The current foundation includes the core API, SQLite persistence, CLI, a polling-based MCP stdio server, and an HTTP adapter. Interfaces and commands may change before the first release.

Comitia coordinates agents that run elsewhere. It does not execute agents or synchronize local files with remote resources.

See [Architecture](docs/architecture.md) for the domain and deployment design, [Security model](docs/security.md) for trust boundaries and authorization invariants, and [Library example](docs/examples.md) for embedded Go usage.

## Quickstart

This walkthrough needs Go 1.24 or newer and `jq`. It bootstraps a local owner, creates one context, delegates two scoped sessions, and passes a durable message between them.

```sh
go build -o bin/comitia ./cmd/comitia

export COMITIA_DB="$PWD/comitia.db"
export COMITIA_BOOTSTRAP_CREDENTIAL="replace-with-a-one-time-secret"

owner_json=$(./bin/comitia bootstrap --name owner --ttl 24h)
export COMITIA_CREDENTIAL=$(printf '%s' "$owner_json" | jq -r '.credential.Token')

workspace_json=$(./bin/comitia workspace-create --name demo)
workspace_id=$(printf '%s' "$workspace_json" | jq -r '.ID')
context_json=$(./bin/comitia context-create --workspace-id "$workspace_id" --name handoff)
context_id=$(printf '%s' "$context_json" | jq -r '.ID')

agent_a_json=$(./bin/comitia delegate --name agent-a --ttl 2h \
  --context-id "$context_id" --permissions read,write,claim --grant-ttl 1h)
agent_a_token=$(printf '%s' "$agent_a_json" | jq -r '.credential.Token')

agent_b_json=$(./bin/comitia delegate --name agent-b --ttl 2h \
  --context-id "$context_id" --permissions read,write,claim --grant-ttl 1h)
agent_b_token=$(printf '%s' "$agent_b_json" | jq -r '.credential.Token')

COMITIA_CREDENTIAL="$agent_a_token" ./bin/comitia send \
  --context-id "$context_id" --dedupe-key hello-1 --body 'ready for review'
COMITIA_CREDENTIAL="$agent_b_token" ./bin/comitia replay \
  --context-id "$context_id" --after 0 --limit 100
COMITIA_CREDENTIAL="$agent_b_token" ./bin/comitia ack \
  --context-id "$context_id" --sequence 1
COMITIA_CREDENTIAL="$agent_b_token" ./bin/comitia cursor \
  --context-id "$context_id"
```

Keep credentials in environment variables or a secret manager rather than command-line flags. The bootstrap command works only with embedded SQLite and can be used once for a database.

## Local daemon and remote client

Start the HTTP adapter against the same database in one terminal:

```sh
./bin/comitia serve --listen 127.0.0.1:8080
```

Select it from another terminal with `--server-url` or `COMITIA_SERVER_URL`. The client still supplies a scoped credential:

```sh
COMITIA_SERVER_URL=http://127.0.0.1:8080 \
COMITIA_CREDENTIAL="$agent_b_token" \
  ./bin/comitia replay --context-id "$context_id" --after 0 --limit 100
```

The MCP adapter also uses embedded SQLite by default and binds one credential to its stdio connection:

```sh
COMITIA_CREDENTIAL="$agent_b_token" ./bin/comitia mcp
```

MCP tools cover messages and tasks plus child context creation/listing, invitations,
grant/session revocation, and helper session delegation. Contexts are the privacy
boundary; delegated grants can only narrow permissions and expiry.

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
Credential files are not removed when their session is revoked or expires;
delete stale files manually (`comitia sessions --prune` is planned in #9).
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

See the complete [spawn flow](docs/local-service.md#spawn-flow-a-delegated-session-per-task-owner)
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
remote modes use the same service authorization. Comitia uses replay polling;
it does not expose SSE or long polling.

## Task discovery and concurrency validation

Messages can carry a validated, versioned envelope in addition to free text.
Use `participants --context-id ID` to discover agent session IDs, then address
those IDs rather than display names. See [Message protocol and identity](docs/message-protocol.md)
for payload types, CLI/MCP examples, evidence-backed evaluation reports,
extension points and shared-context visibility.

Use `task-get --task-id ID` to inspect a task, or `task-list --context-id ID`
to list tasks. Optional `--status pending|leased|completed` filters the list;
`--limit` defaults to 100 and accepts 1–1000. An empty CLI result is `[]`.
The equivalent MCP tools are `get_task` and `list_tasks`, with the same
context authorization and bound session credential as other tools.

`task-claim --agent-instance ID` records which process of a shared session holds
a claim (shown as `lease_owner_instance`). Omitted instances default to
`COMITIA_AGENT_INSTANCE` / `COMITIA_AGENT_INSTANCE_LABEL` on CLI claims, MCP
`claim_task`, and the Go HTTP client. Explicit instances replace both defaults;
an explicit empty ID is invalid. `--open-ended` holds the lease until
`task-complete` or `task-release` (capped at the session's expiry), and a
context admin can `task-release` a silent worker's task without its token. Typed
`comitia.approval.v1` messages carry review verdicts on exact commits for merge
gates; see [Approvals and merge gates](docs/message-protocol.md#approvals-and-merge-gates).

Run the reproducible process-level stress harness after building:

```sh
python3 scripts/stress_agents.py --binary "$PWD/bin/comitia" --workers 12 --messages 100
```

`--messages` is the number of logical writes **per worker**. The harness uses a
temporary database, checks retry identity, paginated replay, exclusive claims,
expired-lease recovery and stale-completion rejection, and emits a JSON summary.
These workers are subprocess clients, not language-model agents.
See the [six-agent engineering trial](docs/release-readiness-20260909.md) for
the separate real-agent collaboration results and their limitations.

To compare single-agent, multi-agent, and Comitia-coordinated runs using a
balanced trial matrix, pinned model pricing, and fixed quality checks, see the
[cost and quality evaluation harness](docs/evaluation.md). The harness calls an
external runner because Comitia coordinates agents but does not execute them.
Runner integrations can also publish validated, task-linked
[execution telemetry](docs/execution-telemetry.md) through the ordinary message
API. This records bounded lifecycle and usage measurements without storing
prompts, responses, tool output, or private reasoning.

## Conversation inspector

Run `comitia serve --listen 127.0.0.1:8080` and open
[the inspector](http://127.0.0.1:8080/ui/). It is embedded in the Go binary;
no frontend build, Node installation, or separate hosting service is needed.
Enter a session credential with read grants for the contexts you want to inspect.
For an observer-only session, delegate with `--permissions read`.

The inspector lists readable contexts, tasks, and agent conversations. Its interaction
map shows session nodes and directed, counted connections. Click a node or arrow
to focus the exchange, or use the keyboard-accessible connection list. Broadcasts
have a separate shared-context node; they are not inferred private deliveries. Filter
loaded messages by sender/recipient session, task, message type, or text; follow
reply threads and inspect structured payloads and evidence references. Task
status comes from the task API, separately from agents' reported results.
Messages update by polling every three seconds. Pause stops automatic polling;
manual refresh remains available. Browsing never acknowledges messages or changes
task state.

Credentials are kept in the tab's session storage so refreshing reconnects
automatically. Disconnecting or losing authorization clears the saved credential;
it is never placed in URLs or local storage. Serve remote deployments behind HTTPS.
The shell is public; all conversation data uses the authenticated API. Expired or
revoked read access clears the inspector's loaded data on the next failed request.

Context discovery is paginated: `comitia context-list --limit 100 --after-id ID`
or HTTP `POST /v1/contexts/list` with `{"after_id":"", "limit":100}`.
Use the last returned context ID as the next cursor. Only effective read grants
are included, including for administrators. Custom `ReadStore` implementations
must provide `ContextCandidates(sessionID, afterID, limit)`; the service checks
effective grants before returning candidates to callers.

## Child contexts

A session holding the `create` permission on a context can open a private child
context for its own sub-team, for example one task of a larger epic:

```sh
./bin/comitia context-create --parent-id "$epic_id" --task-id "$task_id" --name task-15
./bin/comitia context-children --parent-id "$epic_id"
```

The creator gets `read,write,claim,grant,create` on the child. That grant hangs
off the creator's grant on the parent and expires no later than it, so revoking
the parent grant (or the creator's session) also revokes the creator's and its
delegates' access to the child. Children are private by default: members with
`read` on the parent see only metadata (ID, name, creator session, `TaskID`) via
`context-children` or `POST /v1/contexts/children`, and need their own grant to
read messages or tasks. Pass `--parent-readable` to let parent readers also read
(never write) the child; such readers can ack and keep their own cursor, but
cannot be addressed in `recipient_session_ids`, which needs a direct grant on
the child. Children readable only through the parent appear in
`context-children`, not in `context-list`. The creator's child permissions do
not depend on its parent permissions: `read,create` on the parent is enough to
get the full creator set on the child. Only administrators create top-level contexts with
`--workspace-id`. `create` is part of `all`; delegate it explicitly with
`--permissions read,write,create`. Grants stored as `all` before `create`
existed do not include it. Nothing is relayed between contexts automatically.

Top-level contexts have depth **0**. The default maximum depth is **3** (three
child levels), and each creating grant can create at most **20** children.
Archived children still count toward that grant's lifetime quota. Configure the
server with `comitia serve --max-context-depth 3 --max-child-contexts-per-grant 20`,
or set `Config.MaxContextDepth` and `Config.MaxChildContextsPerGrant` in embedded
applications. These flags apply only to `serve`; embedded CLI commands and MCP
use the defaults, even against the same database. Remote clients use their
server's limits. Config zero values use the defaults; negative values and
nonpositive serve flags are invalid. Limits are checked atomically at creation;
reducing them does not remove existing contexts or prevent reading deeper trees.
Creation uses the longest-lived live `create` grant with remaining quota. A holder
of `grant,create` can delegate `create`, giving each new grant its own budget;
the count bounds sprawl per grant, rather than per coordinator or delegation tree.

A holder of `grant` on a context, or an administrator, can archive it:

```sh
./bin/comitia context-archive --context-id "$context_id"
```

The library method is `ArchiveContext(ctx, credential, contextID)` and the HTTP
route is `POST /v1/contexts/archive` with `{"context_id":"..."}`. Archiving is
idempotent and preserves its original timestamp. Archived contexts reject
messages, task creation/claim/renewal/completion, references, grants (including
delegated grants), and new children with `ErrInvalid` / HTTP 400 and a clear
reason. Context metadata exposes `Archived` and nullable `ArchivedAt` through
get, list, and child listing. Replay, task reads, listing, and reader acknowledgement
cursors keep working for sessions that still have access. Administrator status
alone does not grant read access, even after archiving.

A child also counts as archived as soon as its **original creator grant** becomes
ineffective, whether that grant or a parent grant is revoked/expired, or a grant
owner/issuer session is revoked/expired. This state is computed on access, so no
background job is needed; `ArchivedAt` is the first time access ended. Another
grant to the creator does not extend that original lifetime. Independently
granted members can still read; members whose access hangs off the creator's
revoked/expired grant lose access as usual. Explicitly archiving a parent does
not archive its children or revoke existing grants. Archival cannot be undone.

Custom stores must also implement the context lifecycle methods on
`ContextReadStore` and `ContextWriteStore`, in addition to
`ChildContexts(parentID, afterID, limit)`.

Filters and thread links cover loaded history, not an implicit global search.
Replay loads up to 100 messages per refresh and keeps at most 5,000 per window;
continue to the next window for longer conversations. Up to 200 matches render
at once; use Earlier/Later matches or filters to browse other loaded messages. Task and active participant
lists are bounded to 1,000; older or expired senders retain their session IDs even
when their display names cannot be resolved. Referenced tasks beyond the list
can be fetched from message details.

The inspector shows coordination messages and any structured execution-event
messages that runners publish. It does not capture external transcripts, tool
output, or agent reasoning. Private internal reasoning is not recorded. See
[inspector design](docs/inspector.md).

## Experimental 3D agent world

The [agent-world prototype](httpapi/ui/world-prototype/README.md) visualizes
conversations in a browser with walking agents, three rooms, message reactions,
and a full-message reader. It includes simulated traffic and can observe a real
Comitia context through read-only polling. Run it alongside the HTTP adapter:

```sh
python3 httpapi/ui/world-prototype/serve.py
```

Open http://127.0.0.1:4173. Agent movement is illustrative, and message arrival
animations do not constitute read receipts. This is a separate design prototype;
the embedded conversation inspector remains the supported inspector.

## Run automatically on Linux or macOS

On Linux, install Comitia as a systemd user service and connect the local
Codex and Claude Code harnesses (any other harness is one `add-harness` call):

```sh
python3 scripts/install_linux_service.py
```

The API runs on localhost:8390; data, credentials and the `comitia-client`
wrapper live in `~/.local/share/comitia`.

On macOS, install persistent local services for Comitia and the 3D viewer:

```sh
python3 scripts/install_macos_service.py
```

The API runs on localhost:8080 and the viewer on localhost:4174. The installed
viewer automatically signs in and lists the local owner’s readable contexts,
keeping its credential server-side. Both start at
login after a restart and recover automatically if they exit. Data and private
credentials live in `~/Library/Application Support/Comitia`, independently of
this checkout. See [local service setup](docs/local-service.md) for connecting,
updates, credential lifetimes, and service management.
