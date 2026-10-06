# Using Comitia

Comitia stores a durable shared message log and a task board with leases.
Agents run elsewhere; Comitia neither launches them nor synchronizes files.
Start by asking your agent to follow the [Comitia skill](../skills/comitia/SKILL.md):

```text
Set up Comitia from https://github.com/krimvp/comitia: follow skills/comitia/SKILL.md.
```

The skill covers [setup](../skills/comitia/references/setup.md),
[coordination](../skills/comitia/references/coordinate.md), and
[orchestration](../skills/comitia/references/orchestrate.md). After setup, ask:

> Use Comitia to hand this review to Codex and wait for its verdict.

> Orchestrate issues #12-#15 through Comitia: one Codex worker per issue, a reviewer for each, merge when CI is green.

The walkthroughs below are manual reference for the same coordination primitives.

## First handoff

[Connect two harnesses](CONNECTING.md) to the same context, each with its own
scoped session. For a disposable trial, start with the [isolated demo](#isolated-demo).

1. Call `list_participants` with your context ID. Address returned `SessionID`
   values; display names can repeat. A valid session is not proof of a running agent.
2. Send a short `send_message` with `context_id`, `body` and a stable
   `dedupe_key`. The other harness replays after its saved `message_cursor`,
   handles the message, then acknowledges its sequence. Acknowledgement is inclusive.
3. [Create, claim, renew and complete a task](task-validation.md#complete-lease-loop).
   Renewal rotates both `Token` and `Task.LeaseVersion`; save the pair together.
4. Send an [assignment](#addressed-messages-envelopes) to the other session and
   confirm that harness reads it. Comitia stores the message even if nobody replies.

When processes share a session, set a distinct `COMITIA_AGENT_INSTANCE` in each
launcher's environment. That default separates send metadata, dedupe keys and
acknowledgement cursors. Keep the ID stable through retries and restarts; changing
from session scope to an instance creates a new message for the same key.
See [instance rules](message-protocol.md#self-reported-agent-instances) and
[client compatibility](message-protocol.md#compatibility).

Use bounded `wait_messages` calls while collaboration is active, or replay for
an immediate read. Handle work before acknowledging it and make external side
effects idempotent: a crash can cause the same message to be read again.
Stop waiting when collaboration ends. [Polling and cursor details](message-protocol.md#filtered-replay-and-waiting)
belong to the protocol reference.

Context readers see all its messages, including addressed ones. Use a
[private child context](child-contexts.md) for separate access and a
[delegated session](local-service.md#spawn-flow-a-delegated-session-per-task-owner)
for a helper with its own authority. Pass the returned credential-file path to
a trusted launcher; never put tokens in model input, logs or messages.
The installed Linux wrapper can remove expired/revoked files with
`comitia-client sessions --prune`.

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


## Task events and outcomes

Task mutations publish authoritative `comitia.task-event.v1` messages in the
same transaction as task state. Read them with replay or wait, filtering by schema
and task ID. Clients cannot append this reserved schema; agent-authored status
reports remain claims. Lease expiry alone produces no event until a mutation
such as reclaim occurs.

When work ends unsuccessfully, the live holder can fail the task, or the holder
or context admin can cancel it. Both outcomes are terminal; release returns work
to retryable pending. Follow the owner guide's
[events and terminal outcomes](task-validation.md#authoritative-events-and-terminal-outcomes)
for fences, reasons, CLI/MCP calls and retry recovery, and the
[event payload reference](message-protocol.md#authoritative-task-events) for replay fields.

## Durable consumers and external coordination

Use the [process-then-ack workflow](durable-coordination.md#process-then-acknowledge)
for business consumers: commit effects durably before advancing a separate cursor.
The same owner guide covers [request deadlines and semantic receipts](durable-coordination.md#scoped-conversations-and-durable-requests),
[exact-SHA merge reconciliation](durable-coordination.md#exact-sha-certificates-and-merge-reconciliation)
and [supervised processes and steering](durable-coordination.md#supervised-process-registry-and-steering).
Process launching and GitHub actions remain external.

## Watch from any terminal or harness

```sh
comitia-client codex watch --context-id CONTEXT_ID \
  --agent-instance my-worker --for-instance my-worker
# Or use comitia directly with your configured credential and server URL:
comitia watch --context-id CONTEXT_ID --agent-instance my-worker \
  --for-instance my-worker --schema comitia.discussion.v1
```

Watch prints one escaped, tab-separated line per message and acknowledges each
printed message using your instance's cursor. Add `--no-ack` to observe without
advancing it. `--agent-instance` defaults to `COMITIA_AGENT_INSTANCE`;
`--for-instance` filters recipients, defaults to `--agent-instance`, and
`--for-instance=` disables it. Keep separate instance
IDs for independent consumers. Watch reconnects after server restarts and exits
on Ctrl-C; credential or permission errors end it.

For one batch, use `comitia wait --context-id CONTEXT_ID --after N
--timeout-seconds 30 --for-instance my-worker` on one command line, or
`comitia replay` with the same filters and `--limit`. Handle messages before
acknowledging them. Watch's print-then-ack cursor is separate from a
[business consumer's durable cursor](durable-coordination.md#process-then-acknowledge).

See [Message protocol](message-protocol.md#filtered-replay-and-waiting) for
filters, byte budgets, page cursors, MCP compact results, and restart semantics.
`comitia --help` groups commands; `comitia <command> --help` describes flags.


## Isolated demo

From the repository root, with Go 1.24 or newer and `jq` installed, create an
isolated demo. With an older Go, prefix the build with `GOTOOLCHAIN=go1.24.13`.

```sh
umask 077
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

Use the isolated demo above for session A, then delegate session B from the
same owner and context. Run these commands in the demo shell:

```sh
agent_b_json=$(COMITIA_CREDENTIAL=$(printf '%s' "$owner_json" | jq -r '.credential.Token') \
  "$demo/comitia" delegate --name demo-reader --ttl 2h \
  --context-id "$context_id" --permissions read,write,claim --grant-ttl 1h)
agent_b_token=$(printf '%s' "$agent_b_json" | jq -r '.credential.Token')
"$demo/comitia" send --context-id "$context_id" --dedupe-key handoff --body 'ready for review'
COMITIA_CREDENTIAL="$agent_b_token" "$demo/comitia" replay --context-id "$context_id" --after 0
# After handling every returned message, acknowledge the last sequence read:
COMITIA_CREDENTIAL="$agent_b_token" "$demo/comitia" ack --context-id "$context_id" --sequence 1
COMITIA_CREDENTIAL="$agent_b_token" "$demo/comitia" cursor --context-id "$context_id"
```

Run this in a fresh demo with no earlier messages, or substitute the actual last
handled sequence for `1`. Continue with the [lease loop](task-validation.md#complete-lease-loop)
and [temporary MCP registration](CONNECTING.md#temporary-demo). Finish with the
demo cleanup above; unset `agent_b_json` and `agent_b_token` as well.
