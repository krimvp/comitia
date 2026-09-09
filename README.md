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
