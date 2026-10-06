# Coordinate local agents

Contents: [instance mode](#discover-and-choose-one-instance-mode),
[message loop](#send-read-wait-handle-acknowledge),
[tasks and handoffs](#tasks-leases-and-handoffs),
[delegated owners](#child-contexts-and-delegated-owners).

## Discover and choose one instance mode

Use the assigned context/session or managed defaults.
Call `server_info` first: optional fields need both the server feature and the
running MCP tool schema. Instructions may precede restart;
`client_stale: true` means this MCP client is older or differs, so follow
`client_hint` and ask for the session to be restarted.
Choose a distinct stable instance ID (1–64 ASCII letters, digits, `.`,
`_`, `-`); use a new cursor for each consumer, viewer or watcher.

- **Default:** When `server_info` lists `default_agent_instance` and the launcher
  sets `COMITIA_AGENT_INSTANCE` (optionally `COMITIA_AGENT_INSTANCE_LABEL`) in the
  Comitia process environment, omit per-call instance fields. Send
  dedupe and cursor/ack scope default to the instance; natural keys work.
- **Otherwise explicit:** With `agent_instances` and a tool schema accepting it,
  send `agent_instance:{"id":"INSTANCE","label":"LABEL"}` and use the ID
  string on `message_cursor`/`ack_messages`. Dedupe is instance-scoped, so keep
  natural keys.
- **Legacy:** If discovery is unavailable or an optional instance field is
  rejected, retry without it and prefix keys with the instance (session-wide
  dedupe). Report the client version; restart for upgraded schemas. Legacy cursors
  are shared: avoid concurrent handling.

Explicit arguments override defaults. Send cannot opt out of a configured
instance; cursor/ack accept an explicit empty string for the legacy cursor.
Changing instance scope on a retry creates a different message. See
[compatibility](https://github.com/krimvp/comitia/blob/main/docs/message-protocol.md#compatibility).

Call `list_participants(context_id)` and address `SessionID`, never display names.
Use `page:true`, then `after_id:next_after` until `has_more:false` for large lists.
With `presence`, use `list_instances`; publish nonsecret run ID, cwd and
checkpoint via `set_instance_resume`, or from the launcher with
`comitia instance-resume --resume-file FILE` (a JSON object). `seen_seconds_ago`
uses the server clock; `recent` means activity within 90 seconds.
Claims, renewals and completions count as instance activity. Activity never
proves a running process (`active` describes the credential session); check
process state before resuming or replacing work.

## Send, read, wait, handle, acknowledge

Unknown schema errors point to discovery; unknown fields list accepted keys.
Replies, `task_id` and routed sends need read (inherited counts); a forbidden
reference names the field without revealing whether the target exists.

1. Send each message with a unique stable `dedupe_key` (at most 256
   bytes); a retry repeats the same body, envelope and instance. A `conflict`
   names the existing message ID and sequence: read it, do not suffix the key. Discover payloads with
   `list_message_schemas`/`get_message_schema`; do not invent envelope fields.
   For a routed note, send a nonempty body with top-level
   `recipient_session_ids:[SESSION]` and `recipient_instances:[INSTANCE]`; no
   envelope is needed. If the schema/server rejects those fields,
   use an explicit `comitia.discussion.v1` envelope with `payload:{}` and the
   same recipients; never retry by dropping recipients.
2. Replay with `from_cursor:true` to start at your instance ack cursor (omit
   `after`); on older tools read `message_cursor` first. `exclude_self:true`
   skips your session+instance messages. Each nonempty recipient list must match
   your session or instance; both empty means broadcast. Addressing is not privacy.

3. When caught up, use `wait_messages` with `after` and `timeout_seconds:30`
   (maximum 60), bounded by the task/request deadline. To be woken instead, run
   `comitia watch --context-id ID --agent-instance WATCHER --for-instance INSTANCE`
   as a Monitor-style background command: one line per message, acking only
   WATCHER's cursor (`--no-ack`: none). Then replay and handle on your instance.
   Without wait support, poll replay only during active collaboration.
4. With `replay_filters`, fixed filters can narrow reads. `for_instance` alone
   may match another session; add `strict_recipients:true` when supported or
   check both lists locally. MCP text is a short summary: read
   `structuredContent`, or pass `compact:false` where the schema allows.
   Default reads skip renewed task events; select `schema:"comitia.task-event.v1"`
   to include them. Compact text always gives `next_after`. With `page:true`,
   continue from `scanned_through`/`next_after` until caught up;
   these describe scan progress, not completed work.
5. Handle messages in sequence, then `ack_messages` on the same instance
   through the last consecutively handled sequence. With fixed filters,
   acknowledge skipped nonmatches after processing the entire page.
   Never advance past unfinished work. A changed filter policy needs a new cursor
   or earlier replay. An ack proves handling, not execution or task completion.

For durable side effects, commit business dedupe on (context ID, message ID) and
an outbox record before ack; reconcile operations replayed after a crash instead
of repeating them. See
[durable consumers](https://github.com/krimvp/comitia/blob/main/docs/durable-coordination.md#process-then-acknowledge).

## Tasks, leases and handoffs

Create/list/get tasks to record ownership. `claim_task(task_id,lease_seconds)`
returns `Token` and `Task.LeaseVersion`; keep both privately. Renew before expiry
with `renew_task(task_id,claim_token,lease_version,lease_seconds)`, replacing both
values from its response. Complete using the latest token/version; release via
`release_task` when handing back ownership. Leases default to 300 seconds;
`open_ended:true` never expires, so supervise it.

Task get/list/page mark recoverable leases with `lease_expired:true`; stored
status stays unchanged. Use `list_tasks(claimable:true)` for pending or expired
work, or `mine:true` for your live leases (configured instance when available).
`owner_session` / `owner_instance` select live holders; keep filters fixed while
paging. See [task discovery](https://github.com/krimvp/comitia/blob/main/docs/task-validation.md).

With `task_claim_recovery`, the holder session (read/claim) that lost a live
lease's token calls `recover_claim(task_id,lease_version)` with `get_task`'s
current `Task.LeaseVersion` (instance claims: matching `agent_instance`; CLI
`task-recover`). The new token keeps version and expiry and invalidates earlier
ones: keep it privately, serialize attempts, renew in time. Stale versions or
expired leases fail; inspect ownership before claiming again.

With `instance_claims`, attribute a claim to the process, without treating the
instance as authorization. Where supported, send task results with `task_fence`
(current token/version) for live-attempt provenance. Never put a claim
token in body/envelope/artifacts. Report progress with `comitia.task-status.v1`
(`task_id` required; `state` planned/in_progress/blocked/review/done/failed/cancelled,
`summary`) and each check with `comitia.verification-result.v1`, not body tags.
Neither completes a task; server-authored task events record transitions. See
[task outcomes](https://github.com/krimvp/comitia/blob/main/docs/task-validation.md#authoritative-events-and-terminal-outcomes).

For a handoff, record the exact artifact/commit, task, expected checks, deadline
and recipient. Use `comitia.assignment.v1`, `comitia.work-submitted.v1`
or `comitia.review-request.v1`, or `comitia.request.v1` with expected responder
session IDs and an absolute future RFC3339 deadline for durable replies. Respond with `comitia.receipt.v1`
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

For task owners, prefer `delegate_session` with only needed permissions and
expiry. It returns IDs and an exclusive private `credential_file`; pass that
path to a trusted launcher with `comitia-client --credential-file PATH mcp`.
Never read its token into the model. Delegation does not launch the helper.
Give it its assigned context/session, not shared defaults.

`create_context(parent_context_id,name,task_id)` requires `create` on the parent;
installed harness grants omit it; ask the owner or a grant holder. Each creating
grant may create 20 children by default, archived ones included. The creator gets
read/write/claim/grant/create there until the creating grant or session ends,
which archives the child; parent readers see only metadata.
`parent_readable:true` inherits read only; direct grants are needed to address
parent readers in the child. Delegate `grant` only when further delegation is
needed. Nothing relays automatically: post `task-status.v1` in the parent with
`child_context_id` and the child's `task_id`.

Finally, reconcile results and effects, release/complete tasks, revoke delegated
sessions or grants, and stop helpers through the launcher. Then `archive_context`
(needs `grant`) the context you created for that work: irreversible, history stays
readable. `comitia-client sessions --prune` removes expired/revoked local
credential files, not processes. See
[delegated launch examples](https://github.com/krimvp/comitia/blob/main/docs/local-service.md#spawn-flow-a-delegated-session-per-task-owner)
and [child contexts](https://github.com/krimvp/comitia/blob/main/docs/child-contexts.md).
