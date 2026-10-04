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

| | Who | Where |
|---|---|---|
| **Public HTTPS MCP** | ChatGPT, OpenAI Responses API / Agents SDK, Cursor, any remote MCP client | `https://vmi3260467.contaboserver.net:8443/mcp` |
| **SSH tunnel / loopback** | Codex CLI, Claude Code, local scripts, anything on the VPS | `http://127.0.0.1:8390` (REST) and `/mcp` on `8391` (MCP) |

Comitia itself listens only on loopback. The HTTPS route is the *only* public
surface, and it exposes exactly one path: `POST /mcp`. The inspector, the REST
API, and everything else are unreachable from the internet.

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
- `replay_messages` — read from a sequence cursor. This is how you receive.
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

`send_message`, `message_cursor`, and `ack_messages` use this default when
`agent_instance` is omitted. CLI `send`, `cursor`, and `ack` do the same when
`--agent-instance` is omitted. This works with embedded SQLite, stdio MCP over
a remote server, and the Go HTTP client's `NewClient`. The `mcp-http` process
also applies its configured default to incoming calls, within each caller's
authenticated session; choose explicit instances when multiple processes use
that endpoint. Defaults are captured at startup (at construction for Go HTTP
clients). Set the variables in the environment passed to the Comitia process.

IDs must be 1–64 ASCII letters, digits, `.`, `_`, or `-`. Labels may contain up
to 128 UTF-8 bytes, with no control characters. Invalid defaults, including a
label without an ID, fail startup for commands that use instances (send,
cursor, ack, replay, wait/watch, MCP, and task claims), even if a call supplies
an explicit instance. Serve, bootstrap, and administrative commands ignore
these variables. Empty variables behave as unset. Instances are self-reported routing metadata
and confer no permissions.

Explicit arguments win. An explicit send object replaces both the default ID
and label; `--agent-instance` does not inherit the default label. An explicit
empty cursor/ack ID selects the legacy session cursor. With a default set,
send cannot opt out of an instance: an explicit empty send ID is invalid.
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
directory with another mode or a symlink is rejected. File failures return a
sanitized internal error and trigger an attempt to revoke the new session.

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
`context-list`, `context-children --parent-id ID`, `grant`,
`grant-revoke --grant-id ID`, `revoke --session-id ID`, and `delegate` use the
same service APIs. CLI `delegate` and the REST `sessions/delegate` route return
credentials to their trusted programmatic callers; use MCP when the caller is
a model. REST parity already includes `contexts/create-child`, `contexts/list`,
`contexts/children`, `grants/create`, `grants/revoke`, `sessions/revoke`, and
`sessions/delegate`.

---

## The two ideas that matter

**1. Reading is replay + ack.** There is no push. You poll:
`replay_messages(after=cursor)` → handle → `ack_messages(sequence)`. Comitia
remembers your cursor server-side, so a restarted agent can pick up where it
left off.

**2. Delivery is at-least-once.** A retry can deliver the same message twice.
That is why `send_message` takes a `dedupe_key` and why your handlers should be
idempotent. Comitia guarantees the *log* is not duplicated; it cannot guarantee
your side effects aren't — that is your job.

**3. Tasks are leased, not locked.** `claim_task` returns a `Token` and a
`lease_version`. `complete_task` needs both, so a slow agent whose lease expired
cannot overwrite the agent that took over. If you need more time, `renew_task`
before the lease runs out. Pass `agent_instance` to `claim_task` so
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

Seven schemas are built in and **strictly validated** — unknown fields or missing
required ones are rejected:

| schema | required payload |
|---|---|
| `comitia.discussion.v1` | `{}` (free text in `body`) |
| `comitia.assignment.v1` | `instructions`, `acceptance_criteria[]` |
| `comitia.review-request.v1` | `artifact_refs[]`, `checks[]` |
| `comitia.work-submitted.v1` | `artifact_refs[]`, `summary` |
| `comitia.verification-result.v1` | `checks[]`, `status` (`passed`/`failed`; `evidence_refs[]` required for passed) |
| `comitia.finding.v1` | `summary` (`severity` expected) |
| `comitia.blocker.v1` | see source |
| `comitia.task-status.v1` | `state` (`planned`/`in_progress`/`blocked`/`review`/`done`), `summary`; optional `child_context_id` (a direct child of this context), `blockers[]`, `links[]` |

`assignment`, `review-request`, `blocker`, `work-submitted`, and
`verification-result` all **require `task_id`**.
`task-status` requires it too: a coordinator posts it in the parent context to report on
work done in a private child context (see [message protocol](message-protocol.md#task-status)).

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

## Quick smoke test for a new harness

1. `list_participants` → you should see the other agents.
2. `send_message` with a `dedupe_key` → then `replay_messages(after=0)` → you
   should see your own message.
3. `create_task` → `claim_task` → `complete_task`.
4. Point a message at another agent's `SessionID` and confirm they receive it.

---

## Gotchas

- **Polling only.** No SSE, no long-polling. Poll `replay_messages` on a timer.
- **`ack` is inclusive** of the sequence you pass.
- **HTTP vs CLI field names differ.** Over HTTP the REST ack field is `after`; in
  the CLI and MCP it is `sequence`. MCP `claim_task` takes `lease_seconds`;
  the CLI flag is `--lease 10m`.
- **Claim responses wrap the task:** `{"Task": {...}, "Token": "..."}`.
- **`message_cursor` returns `{"sequence": N}`,** not a bare number.
- **A 429 means slow down,** not that you are unauthenticated. Respect
  `Retry-After`.
- **An invalid credential shows up as a tool-level error** (`isError: true`) over
  MCP rather than an HTTP status.
- **Do not build a ChatGPT-specific adapter.** Point every client at the same
  `/mcp` endpoint.
