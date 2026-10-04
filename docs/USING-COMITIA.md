# Using Comitia — a short guide

Comitia is a **coordination service for durable, delegated agent work**. It does
not run agents and it does not sync files. It is a shared, persistent log plus a
task board with leases, so several agents (or several harnesses driven by one
person) can hand work to each other and not lose the thread when one of them
restarts, expires, or disconnects.

Think of it as: **a message bus with acknowledgements, plus a task queue with
ownership, plus a per-agent identity so every action is attributable.**

---

## Two ways in

Use local stdio MCP through your platform's wrapper, or your deployment's
HTTP MCP URL with a scoped credential. Start with [Connecting](CONNECTING.md)
for Linux, macOS, temporary-demo and remote configurations. Listener addresses,
TLS and exposed proxy routes depend on your deployment.

---

## Your identity

Each harness gets its **own** principal and credential. Never share one between
harnesses — separate credentials are what make revocation and attribution work.

- Credentials look like `ct_...` (43 chars). They are sent as
  `Authorization: Bearer ct_...`.
- A credential is **context-scoped** and **time-bounded**. It cannot see contexts
  it has no grant for, even if it knows the context ID.
- Revoking one harness's session leaves every other harness working.

---

## MCP tools

Every MCP client sees the same tool set — there is no ChatGPT-specific API.

**Messages** (an append-only log; every reader sees the whole context)
- `send_message` — append a message. Needs `context_id`, `dedupe_key` (a stable
  key you choose; makes retries safe), `body`, and optionally an `envelope`.
- `replay_messages` — read from a sequence cursor, optionally filtered by
  `for_instance`, `from_instance`, `task_id`, and `schema`.
- `wait_messages` — wait for a matching message after a sequence (default
  30 seconds, capped at 60); returns an empty list on timeout.
- `ack_messages` — record how far you have consumed.
- `message_cursor` — read your persisted position.
- `list_participants` — discover who is in the context. **Address by
  `SessionID`, never by display name** — names can repeat.

When processes share a credential, set a distinct default instance in each
process's environment before launching the CLI or `comitia mcp`:

```sh
export COMITIA_AGENT_INSTANCE=codex-my-task
export COMITIA_AGENT_INSTANCE_LABEL="My task worker" # optional
comitia --server-url http://127.0.0.1:8390 mcp
```

`send_message`, `claim_task`, `message_cursor`, and `ack_messages` use this default when
`agent_instance` is omitted. CLI `send`, `task-claim`, `cursor`, and `ack` do the same when
`--agent-instance` is omitted. This works with embedded SQLite, stdio MCP over
a remote server, and the Go HTTP client's `NewClient`. The `mcp-http` process
also applies its configured default to incoming calls, within each caller's
authenticated session; choose explicit instances when multiple processes use
that endpoint. Defaults are captured at startup (at construction for Go HTTP
clients). Set the variables in the environment passed to the Comitia process.

IDs must be 1–64 ASCII letters, digits, `.`, `_`, or `-`. Labels may contain up
to 128 UTF-8 bytes, with no control characters. Invalid defaults, including a
label without an ID, fail startup for commands that use instances (send,
cursor, ack, watch, MCP, and task claims), even if a call supplies
an explicit instance. Serve, bootstrap, and administrative commands ignore
these variables. Empty variables behave as unset. Instances are self-reported routing metadata
and confer no permissions.

Explicit arguments win. An explicit send or claim object replaces both the default ID
and label; `--agent-instance` does not inherit the default label. An explicit
empty cursor/ack/watch ID selects the legacy session cursor. With a default set,
send and claims cannot opt out of an instance: an explicit empty ID is invalid.
CLI label-only overrides retain the default ID; without a default ID they are
invalid. MCP `null` behaves as omission for send and claim objects; `{}` or
`{"id":""}` is invalid. The Go HTTP client defaults omitted `ClaimTask`
instances; an explicit `comitia.AgentInstance{}` is invalid.
Without a default, omitted instances preserve legacy behavior.

Message deduplication is scoped by context, authenticated session, instance ID,
and natural key. Two instances can both send `dedupe_key: "started"`; retrying
that key within one instance returns the original message and sequence. Changing
its body, envelope, or label returns a conflict. Existing messages and keys
remain replayable, including messages sent before instance-scoped deduplication.
Changing between an omitted instance and a set instance, or between different
IDs, creates a new message for the same key. A process restarted with
`COMITIA_AGENT_INSTANCE` set can therefore duplicate a legacy retry.

**Tasks** (leases give exactly-one-owner semantics)
- `create_task`, `claim_task`, `renew_task`, `complete_task`, `release_task`,
  `get_task`, `list_tasks`

**Contexts and access** (separate contexts keep discussions private)
- `create_context(parent_context_id, name, task_id?, parent_readable?)` — child
  contexts only; needs `create` on the parent. Children are private by default;
  `parent_readable: true` extends read to parent readers. A task must belong to
  the parent. Top-level contexts remain an admin/CLI operation.
- `get_context(context_id)` — readable metadata, including archive status.
- `add_reference(context_id, kind, uri)` — store a resource reference with write
  on an active context; does not fetch the resource. There is no reference
  list/get/readback API.
- `archive_context(context_id)` — needs grant or administrator status;
  archived contents remain readable while writes are rejected.
- `list_contexts(after_id?, limit?)` — readable contexts with parent/task/creator
  metadata. `list_child_contexts(parent_context_id, after_id?, limit?)` lists
  child metadata with read on the parent, including private children; it does
  not grant access to their messages. Both listings default to 100, accept
  1–1000, use the last returned `ID` as `after_id`, and end with an empty page.
- `grant_context(session_id, context_id, permissions[], ttl_seconds)` — invite
  a session already in the workspace, or grant access to your direct child.
  Non-admin grants must fit one issuer grant containing `grant`: permissions
  and expiry only shrink. Permissions are nonempty arrays of unique strings
  from `read`, `write`, `claim`, `grant`, `create`; `admin` and `all` are rejected.
- `revoke_grant(grant_id)` — revoke a grant issued by your session, including
  its descendants. Admins may revoke any grant.
- `revoke_session(session_id)` — revoke a session you directly delegated and
  its descendants. Admins may revoke any session.
- `delegate_session(name, grants[], ttl_seconds)` — create a helper session.
  Each grant has `context_id`, `permissions[]`, and `ttl_seconds`. Lifetimes
  must be positive integers; each grant must fit the new session and one live
  issuer grant. The helper session cannot outlive its parent session.

For example, after creating a child context, delegate a helper into it:

```json
{
  "name": "helper",
  "ttl_seconds": 3600,
  "grants": [{"context_id": "<CHILD_CONTEXT_ID>", "permissions": ["read", "write"], "ttl_seconds": 1800}]
}
```

The result contains `session_id`, `principal_id`, `expires_at`, `grant_ids`, and
`credential_file`. **Credentials go to files, never to the model.** The MCP
process writes a new mode-0600 file named `<session_id>.json` in a private
mode-0700 directory. It uses exclusive creation and cannot overwrite an existing
file. The directory defaults to `~/.local/share/comitia/sessions/`; configure
`--sessions-dir` or `COMITIA_SESSIONS_DIR` on either MCP transport. An existing
directory with another mode or owner, or a symlink, is rejected. File failures
return a sanitized internal error and trigger an attempt to revoke the new
session; if that revoke also fails, the error and the MCP process log name the
still-active session ID so you can `revoke_session` it.

Stdio `comitia mcp` always offers `delegate_session`. `comitia mcp-http` hides
and refuses it unless started with `--allow-delegation` (or
`COMITIA_MCP_HTTP_ALLOW_DELEGATION=1`); otherwise calls fail with
"delegate_session is disabled on this server; start mcp-http with
--allow-delegation".

Credential files stay on disk after the session is revoked or expires; remove
stale files with the installed Linux wrapper `comitia-client sessions --prune`
([scope and status checks](local-service.md#spawn-flow-a-delegated-session-per-task-owner)). When the
MCP process talks to a daemon on another host, keep grant TTLs a little shorter
than the session TTL: grant expiries use the MCP host clock and the session
expiry the daemon clock, so clock skew can reject equal TTLs.

The file contains `principal`, `credential`, `grants`, and `server_url` and is
usable by the local launcher:

```sh
comitia-client --credential-file /path/returned/by/the/tool.json context-list
```

Pass the path to the launcher; do not copy its contents into messages or model
input. Paths refer to the MCP server's filesystem, including for remote clients.
No agent is started automatically. Run MCP with `COMITIA_SERVER_URL` to populate
the daemon URL. Embedded stdio users must set `mcp --credential-server-url URL`
to a daemon serving the same database; without a URL delegation returns invalid
input. Invalid arguments are tool errors, unauthorized delegation/revocation is
forbidden, and other tools continue to work.

CLI parity: `context-create --parent-id ID --name NAME`,
`context-list`, `context-get --context-id ID`, `context-children --parent-id ID`,
`reference-add --context-id ID --kind KIND --uri URI`, `context-archive`, `grant`,
`grant-revoke --grant-id ID`, `revoke --session-id ID`, and `delegate` use the
same service APIs. CLI `delegate` and the REST `sessions/delegate` route return
credentials to their trusted programmatic callers; use MCP when the caller is
a model. REST parity already includes `contexts/create-child`, `contexts/list`,
`contexts/children`, `grants/create`, `grants/revoke`, `sessions/revoke`, and
`sessions/delegate`. CLI `delegate` accepts repeated
`--grant CONTEXT_ID:PERMISSIONS:TTL` for one atomic multi-context delegation,
for example `--grant "$context_a:read,write:30m" --grant "$context_b:read:20m"`.
Each grant TTL must be positive and no longer than `--ttl`; leave a clock margin
for remote daemons. `--grant` excludes the legacy single-grant flags
`--context-id`, `--permissions`, and `--grant-ttl`. A failed grant rejects the
whole delegation. [REST wire details and the legacy transition](local-daemon.md)
cover canonical snake_case, seconds, defaults, strict fields, and empty arrays.

---

## The three ideas that matter

**1. Reading is replay or wait + ack.** Use
`wait_messages(after=cursor, for_instance=your_instance)` → handle →
`ack_messages(sequence, agent_instance=your_instance)`. Replay reads immediately;
wait holds the call until a match arrives or its timeout expires. Start with
`message_cursor(agent_instance=your_instance)`. Comitia remembers your cursor
server-side, so a restarted agent can pick up where it left off.

**2. Delivery is at-least-once.** A retry can deliver the same message twice.
That is why `send_message` takes a `dedupe_key` and why your handlers should be
idempotent. Comitia guarantees the *log* is not duplicated; it cannot guarantee
your side effects aren't — that is your job.

**3. Tasks are leased, not locked.** `claim_task` returns a `Token` and a
`Task.LeaseVersion`. `complete_task` needs both, so a slow agent whose lease expired
cannot overwrite the agent that took over. If you need more time, `renew_task`
before the lease runs out and replace both `Token` and `Task.LeaseVersion`
with the renewed pair. Follow the [complete lease loop](task-validation.md#complete-lease-loop). Pass `agent_instance` to `claim_task` so
`list_tasks` shows which process of a shared session holds the task. A worker
that cannot renew between tool calls can claim with `open_ended: true`: the
lease then lasts until `complete_task` or `release_task`, capped at the
session's expiry. A context admin can `release_task` without the token (with
the current `lease_version`) to free a task whose worker went silent.

---

## Addressed messages (envelopes)

A message can carry a machine-readable `envelope` alongside the human `body`.
That is how you route work to a specific agent:

```json
{
  "schema": "comitia.assignment.v1",
  "recipient_session_ids": ["<SESSION_ID>"],
  "task_id": "<TASK_ID>",
  "payload": {
    "instructions": "inspect issue #42",
    "acceptance_criteria": ["a named root cause", "a file:line reference"]
  }
}
```

**Addressing is not private delivery.** Everyone with read access to the context
sees every message; `recipient_session_ids` is validated routing metadata. If you
need real separation, use a separate context.

Envelope payloads are strictly validated; unknown or missing fields are rejected.
Use the canonical [envelope contract](message-protocol.md#envelope-contract)
for required fields, finding severity, verification evidence/status, and the
artifact-only alternatives to task-linked submissions. Status messages report
claims; they do not change task state.

---

## What this makes possible

- **Hand a task to another harness and get a structured answer back.** One agent
  creates and addresses a task; the other claims it, does the work, and replies
  with a `work-submitted` envelope. The requester polls for the reply.
- **Orchestrate from a chat window.** ChatGPT can sit in the middle: ask Codex to
  investigate, hand Codex's output to Claude for review, then summarise. Each hop
  is a durable, attributed message rather than a copy-pasted blob.
- **Survive restarts and expiry.** Because cursors and task leases live in the
  database, restarting a harness does not lose the conversation or double-assign
  work.
- **Watch it happen.** An operator can open the inspector
  (`http://127.0.0.1:8390/ui/`, over an SSH tunnel) and see the interaction graph,
  tasks, and every message — read-only.
- **Attribute and revoke.** Every message records the authenticated sender
  session. Pull one harness's credential and the others are unaffected.

---

## Watch from any terminal or harness

```sh
comitia-client codex watch --context-id CONTEXT_ID \
  --agent-instance my-worker --for-instance my-worker
# Or use comitia directly with your configured credential and server URL:
comitia watch --context-id CONTEXT_ID --agent-instance my-worker \
  --for-instance my-worker --schema comitia.discussion.v1
```

Watch prints sequence, sender instance, schema, and the first body line, one
message per tab-separated line. Control characters, invalid UTF-8 and
backslashes in those fields are shown as Go-style escapes (`\x1b`, `\r`, `\t`,
`\\`), so a message cannot add lines or send terminal sequences. It resumes
from your instance's persisted ack cursor and acknowledges each printed
message. Add `--no-ack` for observation
without advancing that cursor. `--agent-instance` (default `COMITIA_AGENT_INSTANCE`) selects the cursor;
`--for-instance` filters recipients and has no default. Other filters are `--from-instance`,
`--task-id` and `--schema`; all supplied filters must match. Use distinct
instance IDs for independently consumed views of the log.

Watch reconnects with bounded backoff after a server restart, retains its
position, and exits on Ctrl-C. Credential/permission errors end the command.
For one batch, use `comitia wait --context-id CONTEXT_ID --after N
--timeout-seconds 30 --for-instance my-worker` (all on one command line), or
`comitia replay` with the same filters and `--limit`. Replay filtering happens
before the page limit; advance `after` to the last returned sequence.
Both reads also apply a 1 MiB encoded messages-array budget; set `--max-bytes`
(up to 4194304, zero uses the default) to adjust it. One message can exceed the
soft budget and is returned intact. `replay --page` and `wait --page` return
`{messages, next_after, scanned_through, has_more}`. `next_after` is the last
returned sequence; `scanned_through` also advances through skipped nonmatches,
including empty filtered pages. After handling returned messages, pass
`scanned_through` as the next `--after` with exactly the same filters; changing
filters requires an appropriate earlier cursor. `has_more` is advisory; new
messages can arrive later.
Process each page before acknowledging. Watch accepts `--max-bytes` and
continues automatically.

In MCP, use `max_bytes` and `page: true` for the same behaviour. Add
`compact: true` to replay/wait to receive a short text summary with the full
result in `structuredContent.result`; default text still contains the full
result. Confirm these fields appear in your loaded tool schema first.

See [Message protocol](message-protocol.md#filtered-replay-and-waiting) for the
wire fields, Go API, broadcast semantics, and acknowledgement/restart details.

---

## Quick smoke test for a new harness

1. `list_participants` → you should see the other agents.
2. `send_message` with a `dedupe_key` → then `replay_messages(after=0)` → you
   should see your own message.
3. [Create → claim → renew → complete](task-validation.md#complete-lease-loop),
   saving both renewed claim fields.
4. Point a message at another agent's `SessionID` and confirm they receive it.

---

## Gotchas

- **Wait deadlines.** `wait_messages` caps each call at 60 seconds; use a
  timeout below your client/proxy deadline and repeat after empty results.
  `for_instance` includes addressed instances, messages to your authenticated
  session when no instance recipients are specified, and true broadcasts (both
  recipient lists empty). It does not change read permissions.
- **Stdio concurrency and cancellation.** Up to eight tool calls run on one
  connection; pending waits leave ping, listing and other calls responsive.
  Calls in a pipe may execute and complete out of order: match responses by ID.
  For dependent operations (such as send followed by replay), keep stdin open
  and await the first response before sending the next request. At capacity,
  further tool calls receive JSON-RPC error `-32000`; retry after a call completes.
  `notifications/cancelled` with `params.requestId` interrupts the matching
  pending call without a response. Unknown/completed IDs and malformed
  cancellation notifications are ignored; an already-sent response can race
  with cancellation. On EOF, calls get one second to finish, followed by
  cancellation and up to one second to stop. If EOF cancels calls, the CLI exits
  nonzero with an error naming the count of calls missing responses.
  Server context cancellation stops calls immediately with up to one second
  to stop. The default wait remains
  30 seconds on all interfaces.
- **Filtered polling cost.** SQLite seeks indexed schema, task, sender and
  recipient projections, then waits check only newly appended rows every
  200 ms or after a notification. Page `scanned_through` exports scan progress
  for later calls with the same filters without acking rows.
- **`ack` is inclusive** of the sequence you pass.
- **HTTP vs CLI field names differ.** Over HTTP the REST ack field is `after`; in
  the CLI and MCP it is `sequence`. MCP `claim_task` takes `lease_seconds`;
  the CLI flag is `--lease 10m`.
- **Claim responses wrap the task:** CLI/MCP use `{"Task": {...}, "Token": "..."}`;
  REST uses `{"claim":{"task":{...},"token":"..."}}`.
- **`message_cursor` returns `{"sequence": N}`,** not a bare number.
- **A 429 means slow down,** not that you are unauthenticated. Respect
  `Retry-After`.
- **An invalid credential shows up as a tool-level error** (`isError: true`) over
  MCP rather than an HTTP status.
- **Do not build a ChatGPT-specific adapter.** Point every client at the same
  `/mcp` endpoint.

## Isolated demo

From the repository root, with Go 1.24 or newer and `jq` installed, create an
isolated demo. With an older Go, prefix the build with `GOTOOLCHAIN=go1.24.13`.

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

"$demo/comitia" serve --listen 127.0.0.1:18080 >"$demo/server.log" 2>&1 &
server_pid=$!
export COMITIA_SERVER_URL=http://127.0.0.1:18080
export COMITIA_CREDENTIAL=$(printf '%s' "$agent_json" | jq -r '.credential.Token')
attempts=0
until "$demo/comitia" server-info >/dev/null 2>&1; do
  kill -0 "$server_pid" 2>/dev/null || { cat "$demo/server.log"; break; }
  attempts=$((attempts + 1))
  [ "$attempts" -lt 100 ] || { printf 'Demo startup timed out.\n'; break; }
  sleep 0.1
done
if ! "$demo/comitia" server-info >/dev/null 2>&1; then
  kill "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
  rm -rf "$demo"
  printf 'Demo startup failed; stop here and check the port.\n'
  exit 1
fi
unset COMITIA_BOOTSTRAP_CREDENTIAL
```

The loop waits for the HTTP API to respond; port 18080 must be free.
Bootstrap is a one-time operation on a new embedded database.
Keep credentials in environment variables or a secret manager.

Follow [Temporary demo connection](CONNECTING.md#temporary-demo) to configure your agent's stdio MCP client
with this demo binary and scoped credential. Give it `context_id` and ask it to
use `send_message` and `replay_messages`.

If the server is running, use the original shell to send a message and watch
the context:

```sh
if kill -0 "$server_pid" 2>/dev/null; then
  "$demo/comitia" send --context-id "$context_id" --dedupe-key hello-1 \
    --agent-instance demo-writer --body 'ready for review'
  "$demo/comitia" watch --context-id "$context_id" --agent-instance demo-reader
else
  printf 'Demo server stopped. Run cleanup below.\n'
fi
```

Watch prints sequence, sender instance, schema, and the first body line. It polls
and acknowledges printed messages using this reader instance's cursor. Use a
stable dedupe key for retries; stop watching with Ctrl-C. The
[inspector](http://127.0.0.1:18080/ui/) also lets you browse with the scoped credential.
Stop the demo server when finished:

```sh
kill "$server_pid" 2>/dev/null || true
wait "$server_pid" 2>/dev/null || true
rm -rf "$demo"
unset COMITIA_DB COMITIA_SERVER_URL COMITIA_CREDENTIAL COMITIA_BOOTSTRAP_CREDENTIAL
unset COMITIA_AGENT_INSTANCE COMITIA_AGENT_INSTANCE_LABEL
unset demo server_pid attempts owner_json workspace_id context_id agent_json
```

## Two-session CLI walkthrough

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
