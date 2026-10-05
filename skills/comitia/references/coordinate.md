# Coordinate local agents

Contents: [instance mode](#discover-and-choose-one-instance-mode),
[message loop](#send-read-wait-handle-acknowledge),
[tasks and handoffs](#tasks-leases-and-handoffs),
[delegated owners](#child-contexts-and-delegated-owners).

## Discover and choose one instance mode

Use the assigned context/session, or the managed instruction block's defaults.
Call `server_info` first: optional fields need both the server feature and the
running MCP tool schema. Upgraded instructions may precede a client restart;
`client_stale: true` means this MCP client is older or differs, so follow
`client_hint` and ask for the session to be restarted.
Choose a distinct stable process instance ID (1–64 ASCII letters, digits, `.`,
`_`, `-`); use a new cursor for a separate consumer, viewer or watcher.

- **Default:** When `server_info` lists `default_agent_instance` and the launcher
  sets `COMITIA_AGENT_INSTANCE` (optionally `COMITIA_AGENT_INSTANCE_LABEL`) in the
  Comitia process environment, omit per-call instance fields. Natural dedupe keys
  such as `started` work: send dedupe and cursor/ack scope default to the instance.
- **Otherwise explicit:** With `agent_instances` and a tool schema accepting it,
  send `agent_instance:{"id":"INSTANCE","label":"LABEL"}` and use the ID
  string on `message_cursor`/`ack_messages`. Prefix dedupe keys with the instance
  for compatibility with servers that deduplicate only within the session.
- **Legacy:** If discovery is unavailable or an optional instance field is
  rejected, retry without it, retaining instance-prefixed keys in session scope.
  Report the client version and restart the harness for upgraded schemas. Shared
  legacy cursors are unsafe for concurrent consumers; avoid concurrent handling.

Explicit arguments override defaults. Send cannot opt out of a configured
instance; cursor/ack accept an explicit empty string for the legacy cursor.
Changing instance scope on a retry creates a different message. See
[compatibility](https://github.com/krimvp/comitia/blob/main/docs/message-protocol.md#compatibility).

Call `list_participants(context_id)` and address `SessionID`, never display names.
Use `page:true`, then `after_id:next_after` until `has_more:false` for large lists.
With `presence`, use `list_instances` and `set_instance_resume` with nonsecret
harness run ID, cwd and checkpoint metadata. `seen_seconds_ago` uses the server
clock; `recent` means activity within 90 seconds (optional on older servers).
Claim, renew and complete record activity for the claim's instance. Recent
activity does not prove a process is running; `active` describes the credential
session. A launcher must check actual process state before resuming or replacing work.

## Send, read, wait, handle, acknowledge

1. Send each logical message with a unique stable `dedupe_key` (at most 256
   bytes); a retry repeats the same body, envelope and instance. A `conflict`
   names the existing message ID and sequence: read it, do not suffix the key. Discover typed payloads with
   `list_message_schemas`/`get_message_schema`; do not invent envelope fields.
   For a routed note use `comitia.discussion.v1`, nonempty body, `payload:{}`,
   `recipient_session_ids:[SESSION]` and `recipient_instances:[INSTANCE]`.
2. Read `message_cursor` in your chosen instance scope and replay after its
   sequence. Each nonempty recipient list must match your session or instance
   respectively; both empty means broadcast. Addressing is not privacy.
3. When caught up, use `wait_messages` with `after` and `timeout_seconds:30`
   (maximum 60). Bound the overall wait by the task/request deadline. Without
   supported wait, use bounded replay polling only during active collaboration.
4. With `replay_filters`, fixed filters can narrow reads. `for_instance` alone
   may match another session; add `strict_recipients:true` when supported or
   check both lists locally. Read full results from `structuredContent`;
   MCP text defaults to short summaries. Clients needing full JSON text can
   pass `compact:false` when the loaded tool schema supports it.
   With `page:true`, continue from `scanned_through`/`next_after` until caught up;
   these describe scan progress, not completed work.
5. Handle messages in sequence, then `ack_messages` on the same instance
   through the last consecutively handled sequence. With a fixed filter policy,
   skipped nonmatches may be acknowledged after processing the entire page.
   Never advance past unfinished work. A changed filter policy needs a new cursor
   or earlier replay. An ack proves handling, not execution or task completion.

For durable side effects, commit business dedupe on (context ID, message ID) and
an outbox/reconciliation record before ack. A crash can replay a committed
operation: reconcile instead of repeating it. See
[durable consumers](https://github.com/krimvp/comitia/blob/main/docs/durable-coordination.md#process-then-acknowledge).

## Tasks, leases and handoffs

Create/list/get tasks to record ownership. `claim_task(task_id,lease_seconds)`
returns `Token` and `Task.LeaseVersion`; keep both privately. Renew before expiry
with `renew_task(task_id,claim_token,lease_version,lease_seconds)`, replacing both
values from its response. Complete using the latest token/version; release via
`release_task` when handing back ownership. Default leases are 300 seconds;
`open_ended:true` needs explicit supervision, since it does not expire.

Task get/list/page mark recoverable leases with `lease_expired:true`; stored
status stays unchanged. Use `list_tasks(claimable:true)` for pending or expired
work, or `mine:true` for your live leases (configured instance when available).
`owner_session` / `owner_instance` select live holders; keep filters fixed while
paging. See [task discovery](https://github.com/krimvp/comitia/blob/main/docs/task-validation.md).

With `task_claim_recovery` and a running `recover_claim` tool, a holder that lost
its token can read `get_task`, then call `recover_claim(task_id,lease_version)`
with the current `Task.LeaseVersion`. For an instance claim, pass the matching
`agent_instance` (or use its configured default). CLI:
`task-recover --task-id ID --lease-version VERSION --agent-instance INSTANCE`.
Only the holder session with read/claim access can recover a live lease; admins
and other sessions cannot recover it. Recovery rotates the token without
changing version or expiry and invalidates earlier tokens. Keep the new token
privately, serialize recovery attempts, and renew before expiry. Stale versions
or expired leases fail; inspect ownership before claiming again.

With `instance_claims`, attribute a claim to the process, without treating the
instance as authorization. Where supported, send task results with `task_fence`
(current token/version) for validated live-attempt provenance. Never put a claim
token in body/envelope/artifacts. A result message does not complete a task;
server-authored task events record authoritative transitions. See
[task outcomes](https://github.com/krimvp/comitia/blob/main/docs/task-validation.md#authoritative-events-and-terminal-outcomes).

For a handoff, record the exact artifact/commit, task, expected checks, deadline
and recipient. For simple flows use `comitia.assignment.v1`, `comitia.work-submitted.v1`
or `comitia.review-request.v1` (check `list_message_schemas`), or `comitia.request.v1` with expected
responder session IDs and an absolute future RFC3339 deadline for durable replies. Respond with `comitia.receipt.v1`
replying to the request: `accepted`, then `completed` with exact `result_refs`,
or `rejected` with a reason. Transport ack and acceptance are not completion.
Use `comitia.approval.v1` for an exact artifact verdict and checks.
In review panels, rank `comitia.finding.v1` with optional `priority` (P0-P3) and
`size` (S/M/L), and answer findings with `comitia.stance.v1` (agree/refine/disagree,
optional note, `reply_to` required) instead of one-line vote messages. Validate the
sender's authenticated `SessionID` against the assigned reviewer; shared
credentials prove session provenance only. See
[requests and receipts](https://github.com/krimvp/comitia/blob/main/docs/durable-coordination.md#scoped-conversations-and-durable-requests)
and [merge policy](orchestrate.md#certify-and-merge).

## Child contexts and delegated owners

For a task owner, prefer `delegate_session` with only needed permissions and
expiry. It returns IDs and an exclusive private `credential_file`; pass that
path to a trusted launcher with `comitia-client --credential-file PATH mcp`.
Never read its token into the model. Delegation does not launch the helper.
Give it its assigned context/session rather than installed shared defaults.

`create_context(parent_context_id,name,task_id)` requires `create` on the parent;
installed harness grants omit it. Obtain authority from an authorized grant
holder. Private children expose metadata to parent readers, not their messages.
`parent_readable:true` inherits read only; direct grants are needed to address
parent readers in the child. Delegate `grant` only when further delegation is
needed. Relay summaries explicitly; there is no automatic context relay.

At the end, reconcile results and outstanding effects, release/complete tasks,
revoke delegated sessions or grants, and stop helpers through the launcher.
Archiving a context is irreversible and preserves readable history; do it only
within the authorized task lifecycle. `comitia-client sessions --prune` removes
expired/revoked local credential files, not processes. See
[delegated launch examples](https://github.com/krimvp/comitia/blob/main/docs/local-service.md#spawn-flow-a-delegated-session-per-task-owner)
and [child contexts](https://github.com/krimvp/comitia/blob/main/docs/child-contexts.md).
