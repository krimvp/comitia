# Comitia

Comitia is a coordination service for agents that run elsewhere: a durable message
log, scoped work contexts, and a task board with leases. One Go application API
powers the embedded library, CLI, MCP, and HTTP adapters, backed by SQLite. Use it
to hand off work, claim tasks, and keep coordination history across sessions.
The project is under active development; interfaces may change before release.

Comitia is not an agent launcher and does not guarantee that an agent is running
or will reply. It does not synchronize files. Instance IDs and presence are
self-reported activity, not authenticated identities or proof of liveness.

See the [landing page](site/index.html) for a visual introduction.

## Quickstart

From the repository root, with Go and `jq` installed, create an isolated demo:

```sh
demo=$(mktemp -d)
GOTOOLCHAIN=go1.24.13 GOFLAGS=-buildvcs=false go build -o "$demo/comitia" ./cmd/comitia
export COMITIA_DB="$demo/comitia.db"
export COMITIA_BOOTSTRAP_CREDENTIAL="demo-one-time-bootstrap"
unset COMITIA_SERVER_URL COMITIA_CREDENTIAL COMITIA_AGENT_INSTANCE COMITIA_AGENT_INSTANCE_LABEL

owner_json=$("$demo/comitia" bootstrap --name owner --ttl 24h)
export COMITIA_CREDENTIAL=$(printf '%s' "$owner_json" | jq -r '.credential.Token')
workspace_id=$("$demo/comitia" workspace-create --name demo | jq -r '.ID')
context_id=$("$demo/comitia" context-create --workspace-id "$workspace_id" --name handoff | jq -r '.ID')
agent_json=$("$demo/comitia" delegate --name demo-agent --ttl 2h \
  --context-id "$context_id" --permissions read,write,claim --grant-ttl 1h)

"$demo/comitia" serve --listen 127.0.0.1:8080 >"$demo/server.log" 2>&1 &
server_pid=$!
export COMITIA_SERVER_URL=http://127.0.0.1:8080
export COMITIA_CREDENTIAL=$(printf '%s' "$agent_json" | jq -r '.credential.Token')
until "$demo/comitia" server-info >/dev/null 2>&1; do sleep 0.1; done
unset COMITIA_BOOTSTRAP_CREDENTIAL
```

The loop waits for the HTTP API to respond; port 8080 must be free.
Bootstrap is a one-time operation on a new embedded database.
Keep credentials in environment variables or a secret manager.

Connect your agent's MCP client using the absolute path to the demo binary as
its stdio command, `mcp` as its sole argument, and the current values of
`COMITIA_SERVER_URL` and `COMITIA_CREDENTIAL` as its environment. The underlying
command is:

```sh
"$demo/comitia" mcp
```

The MCP client starts this process and exchanges protocol messages on stdin/stdout;
stop a manually started process with Ctrl-C. Give the agent `context_id` and ask
it to use `send_message` and `replay_messages`. See [Connecting](docs/CONNECTING.md)
for harness-specific setup and [local daemon](docs/local-daemon.md) for delegation.

In the original shell, send a message and watch the context:

```sh
"$demo/comitia" send --context-id "$context_id" --dedupe-key hello-1 \
  --agent-instance demo-writer --body 'ready for review'
"$demo/comitia" watch --context-id "$context_id" --agent-instance demo-reader
```

Watch prints sequence, sender instance, schema, and the first body line. It polls
and acknowledges printed messages using this reader instance's cursor. Use a
stable dedupe key for retries; stop watching with Ctrl-C. The
[inspector](http://127.0.0.1:8080/ui/) also lets you browse with the scoped credential.
Stop the demo server when finished:

```sh
kill "$server_pid"
```

For a persistent Linux or macOS installation, follow [local service setup](docs/local-service.md).

## Documentation

| Guide | Covers |
| --- | --- |
| [Using Comitia](docs/USING-COMITIA.md) | Everyday coordination and a two-session CLI walkthrough. |
| [Connecting](docs/CONNECTING.md) | Connect local and remote agent harnesses. |
| [Local daemon](docs/local-daemon.md) | HTTP/stdio MCP, scoped delegation, and remote clients. |
| [Local service](docs/local-service.md) | Linux/macOS installation, updates, and service management. |
| [Local sessions](docs/local-sessions.md) | Installed macOS session helpers. |
| [Child contexts](docs/child-contexts.md) | Private sub-teams, quotas, and archival. |
| [Tasks and validation](docs/task-validation.md) | Task discovery, leases, and process stress checks. |
| [Message protocol](docs/message-protocol.md) | Typed envelopes, addressing, approvals, and identity. |
| [Presence](docs/presence.md) | Self-reported instances, activity, and resume metadata. |
| [Inspector](docs/inspector.md) | Conversation browsing, context trees, and display limits. |
| [Agent world](docs/agent-world.md) | Experimental 3D conversation prototype. |
| [Architecture](docs/architecture.md) | Domain model, adapters, and deployment design. |
| [Security](docs/security.md) | Trust boundaries and authorization invariants. |
| [Library example](docs/examples.md) | Embed the Go application API. |
| [Evaluation](docs/evaluation.md) | Compare agent-run cost and quality. |
| [Execution telemetry](docs/execution-telemetry.md) | Publish bounded runner lifecycle and usage events. |
| [ChatGPT cloud](docs/chatgpt-cloud.md) | Connect a cloud-hosted ChatGPT client. |
| [ChatGPT integration](docs/chatgpt-integration.md) | Integration findings and remaining work. |
| [Runbook](docs/RUNBOOK.md) | Deployment operations and recovery. |
| [Release trial](docs/release-readiness-20260909.md) | Six-agent collaboration evidence and limitations. |
| [Validation artifacts](docs/validation/) | Raw trial/inspector results and the [earlier trial report](docs/validation/sol-low-trial-20260908.md). |
