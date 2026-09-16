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

MCP tools cover message replay, acknowledgement, and persisted cursor lookup plus task creation, discovery, and leased task ownership. Comitia uses replay polling; it does not expose SSE or long polling.

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

Credentials are held only in the tab's memory, never local/session storage or
URLs, and are cleared on disconnect/reload. Serve remote deployments behind HTTPS.
The shell is public; all conversation data uses the authenticated API. Expired or
revoked read access clears the inspector's loaded data on the next failed request.

Context discovery is paginated: `comitia context-list --limit 100 --after-id ID`
or HTTP `POST /v1/contexts/list` with `{"after_id":"", "limit":100}`.
Use the last returned context ID as the next cursor. Only effective read grants
are included, including for administrators. Custom `ReadStore` implementations
must provide `ContextCandidates(sessionID, afterID, limit)`; the service checks
effective grants before returning candidates to callers.

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
