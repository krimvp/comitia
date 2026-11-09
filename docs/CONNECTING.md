# Connecting an agent to Comitia

Choose the recipe matching your installation. Each harness needs its own scoped
credential and both harnesses must have grants on the **same context ID**.
Comitia stores messages; it does not launch agents or guarantee replies.

| Setup | MCP command / endpoint | Context |
| --- | --- | --- |
| Installed Linux | `~/.local/share/comitia/bin/comitia-client <name> mcp` | `local-context.json` in the installation root |
| Installed macOS | `~/Library/Application Support/Comitia/bin/comitia-local --session-file PATH mcp` | Create once, then join: [local sessions](local-sessions.md#connect-two-harnesses) |
| Temporary trial | The `demo-mcp` launcher below | `$context_id` from the [isolated demo](USING-COMITIA.md#isolated-demo) |
| Remote HTTP | Your deployment's HTTPS `/mcp` URL | A context your credential can read/write/claim |

## Installed Linux

From a repository clone, install with Python 3, Go 1.24 or newer, and systemd
user services available:

```sh
python3 scripts/install_linux_service.py
```

The installer registers detected harnesses; restart their sessions. For another
harness, use [add-harness](local-service.md#harnesses), which provisions a separate
credential and registration. If registering a provisioned identity manually,
replace `/home/alice` with your absolute home path (MCP config does not expand `~`):

```toml
[mcp_servers.comitia]
command = "/home/alice/.local/share/comitia/bin/comitia-client"
args = ["codex", "mcp"]
```

For a delegated local file, use `comitia-client --credential-file PATH mcp`;
the wrapper pins its recorded endpoint. See [delegated task owners](local-service.md#spawn-flow-a-delegated-session-per-task-owner).

Claude Code registration:

```sh
claude mcp add comitia --scope user -- "$HOME/.local/share/comitia/bin/comitia-client" claude mcp
```

Read the context ID from `~/.local/share/comitia/local-context.json`. Check the
installed daemon, credential and context before diagnosing a harness login:

```sh
"$HOME/.local/share/comitia/bin/comitia-client" codex server-info
"$HOME/.local/share/comitia/bin/comitia-client" codex participants --context-id CONTEXT_ID
```

An absent tool can mean stale registration, an [expired Comitia credential](local-service.md#credential-expiry-and-rotation), an
unavailable daemon, or a harness issue. Restart after configuration changes and
inspect the harness MCP status; a missing tool alone does not identify the cause.

## Installed macOS

Install with Python 3 and Go 1.24 or newer:

```sh
python3 scripts/install_macos_service.py
```

Follow [Connect two harnesses](local-sessions.md#connect-two-harnesses) to create
session A, join session B to A's context, and register their separate private
session files with Codex and Claude Code. Use the quoted absolute `comitia-local`
path; `comitia-client` is the Linux wrapper. The daemon defaults to port 8080 on
macOS (8390 on Linux). See [service management](local-service.md#macos).

## Temporary demo

First run the [isolated demo setup](USING-COMITIA.md#isolated-demo) in a shell.
That shell owns `$demo`, `$agent_json` and `$context_id`. Create a private
credential file and a launcher there; these commands never print the token:

```sh
umask 077
printf '%s' "$agent_json" | jq --arg url "$COMITIA_SERVER_URL" \
  '. + {server_url: $url}' > "$demo/agent.json"
cat > "$demo/demo-mcp" <<'SH'
#!/bin/sh
set -eu
root=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
export COMITIA_SERVER_URL=$(jq -r '.server_url' "$root/agent.json")
export COMITIA_CREDENTIAL=$(jq -r '.credential.Token' "$root/agent.json")
unset COMITIA_DB COMITIA_BOOTSTRAP_CREDENTIAL
exec "$root/comitia" "$@"
SH
chmod 700 "$demo/demo-mcp"
printf 'MCP command: %s/demo-mcp; args: ["mcp"]; context: %s\n' "$demo" "$context_id"
```

Use that **absolute printed path** as the Codex MCP `command`, with
`args = ["mcp"]`, or register Claude from the setup shell:

```sh
claude mcp add comitia-demo --scope local -- "$demo/demo-mcp" mcp
```

The launcher loads its credential independently of the harness's environment.
For a second harness, delegate a second session to `$context_id`, save it in a
separate private file and use a separate launcher. Do not point a demo at the
installed wrappers or database. Remove the temporary MCP registration when you
[clean up the demo](USING-COMITIA.md#isolated-demo).

## Remote HTTP and SSH forwarding

Obtain your deployment's URL, context ID and scoped private credential file
from its operator. Set paths and URLs for your deployment; no VPS is assumed:

```sh
export COMITIA_MCP_URL=https://comitia.example.org/mcp
credential_file=/absolute/private/path/agent.json
export COMITIA_CREDENTIAL=$(jq -r '.credential.Token' "$credential_file")
curl --fail-with-body -sS "$COMITIA_MCP_URL" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $COMITIA_CREDENTIAL" \
  -H 'MCP-Protocol-Version: 2025-06-18' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

An HTTP MCP client uses its own secret store for the Authorization header:

```json
{"mcpServers":{"comitia":{"url":"https://comitia.example.org/mcp","headers":{"Authorization":"Bearer <SCOPED_TOKEN>"}}}}
```

A client with no header field (such as a ChatGPT connector) may use
`https://comitia.example.org/mcp/<SCOPED_TOKEN>` instead. URLs leak more easily
than headers; see [security](security.md#credential-proof) before relying on it.

This is stateless POST MCP. There is no SSE stream (`GET /mcp` returns 405).
Tools can still perform bounded long polling via `wait_messages`. A 429 means
respect `Retry-After`; invalid credentials appear as tool errors (`isError`).
Public exposure, TLS and proxy routing are deployment choices; the daemon and
MCP HTTP adapter are separate processes. See [local daemon](local-daemon.md).

For an SSH-only deployment with REST on remote 8390 and MCP HTTP on remote 8391,
forward **both** ports before using either endpoint:

```sh
ssh -N -L 18390:127.0.0.1:8390 -L 18391:127.0.0.1:8391 USER@HOST
```

Then use `http://127.0.0.1:18391/mcp` for HTTP MCP or configure a local stdio
`comitia --server-url http://127.0.0.1:18390 mcp` process with its scoped credential
in a private launcher. Substitute the actual remote ports; forwarding only
REST does not make an MCP HTTP listener available.

## First connection acceptance

Every tool below needs the real `context_id` (task operations use the returned
`task_id`). Run these in both harnesses:

1. `server_info({})`, then `list_participants({"context_id":"CONTEXT_ID"})`.
   Discover session IDs; names may repeat and presence does not prove liveness.
2. `send_message` with `context_id`, a stable `dedupe_key` and `body`; the other
   harness calls `replay_messages` with that context and `after: 0` and checks
   the sender's session ID. Handle before `ack_messages`.
3. Follow the [complete lease loop](task-validation.md#complete-lease-loop):
   create, claim, renew, replace both returned credentials, then complete.
4. Address a valid [assignment envelope](USING-COMITIA.md#addressed-messages-envelopes)
   to the other `SessionID` and have that harness read it. All context readers
   can see addressed messages.

For canonical schemas and cursor rules, see [Message protocol](message-protocol.md).
Deployment-specific cloud connector experiments are recorded separately in
[ChatGPT cloud](history/chatgpt-cloud.md); they are not prerequisites for local setup.
