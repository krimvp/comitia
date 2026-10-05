# Durable consumers, requests and external gates

Comitia persists messages, authenticated sender sessions, per-instance cursors and
task leases. Durable business handling, process execution and GitHub operations
belong to your consumer, launcher or merge gate. A message acknowledgement means
“this consumer has handled everything through this sequence”; it does not mean a
worker ran, a request completed or a PR merged.

## Process, then acknowledge

Give the business consumer its own instance, such as `issue-65-consumer`. Use
another instance for a terminal viewer and another for a process watcher. Cursor
ownership is (authenticated session, context, instance); concurrent consumers
must not share one cursor. Reusing the same credential does not authenticate an
instance as a particular process.

For Go/MCP/HTTP consumers:

1. Read `message_cursor` for the consumer instance.
2. Replay pages after that cursor; use wait when caught up. Keep filters fixed
   for this consumer. Handle every returned message in sequence.
3. In your durable business store, deduplicate on (context ID, message ID),
   apply the effect, and commit. For a remote side effect, persist an outbox
   operation and use the remote system's idempotency or reconciliation contract.
4. Only after that commit, call `ack_messages` on the same instance, through
   the last consecutively handled sequence. Do not acknowledge a later message
   while earlier work is pending. Retry a lost ack response with the same sequence.
5. Continue from `scanned_through` for the same filters. It is read progress,
   not business completion. After handling the whole page, a consumer with a
   fixed filter policy may ack through skipped nonmatches too. Changing that
   policy requires a new cursor or replay from an earlier position.

A crash before the effect commits leaves the ack behind, so replay handles the
message. A crash after commit but before ack replays it too; business dedupe skips
the effect and retries ack. This provides at-least-once delivery, not an atomic
transaction between Comitia and your business database.

For a streaming consumer:

```sh
comitia watch --context-id CONTEXT --agent-instance issue-65-consumer \
  --json-lines --manual-ack
# After your handler's durable commit, using the same configured credential:
comitia ack --context-id CONTEXT --agent-instance issue-65-consumer --sequence 43
```

Each output line is a complete Message JSON record, including `ID`, `ContextID`,
authenticated `SessionID`, `Sequence`, `CreatedAt`, the complete multiline
`Body`, `envelope`, `agent_instance`, its self-reported marker and optional
validated `task_attempt`. Newlines, tabs and ASCII control characters are JSON escaped. Decode JSON; do
not parse terminal previews or infer authorization from labels. Secrets such as
credentials and claim tokens are not output. `--json-lines` requires
`--manual-ack` or `--no-ack`; neither advances the persisted cursor. The watch
still advances its in-memory position and resumes from the persisted ack when
restarted. A broken output stream exits without acknowledging the failed write.

The default terminal watch retains its print-then-ack behavior. Its cursor proves
only terminal display. Avoid feeding its automatic acknowledgements into business
completion logic.

## Scoped conversations and durable requests

`get_message(context_id, message_id)`, CLI `message-get --context-id ID
--message-id ID`, HTTP POST `/v1/messages/get` with those two snake_case fields,
and Go's optional `MessageQueryAPI.GetMessage(ctx, token, contextID, messageID)`
read one message under the context's read permission. Missing or foreign message
IDs return forbidden, so lookup cannot reveal messages in other contexts.

Replay/wait/page/watch support these AND-combined optional filters:

| Wire/MCP field | CLI flag | Meaning |
| --- | --- | --- |
| `reply_to` | `--reply-to` | Direct replies to this message in this context |
| `thread_id` | `--thread-id` | This root message and all recursive reply descendants |
| `from_session` | `--from-session` | Authenticated sender session, unlike self-reported `from_instance` |

Unknown or foreign reply/thread roots return forbidden. `from_session` may name
a historical revoked/expired author; it matches immutable provenance, not current
presence. Server-authored `comitia.task-event.v1` messages carry the session
whose task mutation was authorized, so `from_session` matches them too; add
`schema` to exclude them. Task events never reply to anything. Queries require read permission, preserve count/byte limits and
`scanned_through`, and never narrow what another context reader can see.
SQLite uses a new indexed `message_replies` side table and an author index.
Custom stores must opt into `MessageQueryStore.SupportsMessageQueries()` and honor
all three filters; otherwise scoped reads return an explicit unsupported error.
No existing exported interface gains methods. `LookupMessage` handles optional
API capability detection.

New HTTP clients probe `/v1/messages/query-support` before scoped reads or lookup.
Old servers return a clear “unsupported by this server” error; clients never
retry without filters. This protects against both strict endpoint DTO servers
and older servers that ignore unknown union fields. Unset new fields are omitted,
and old clients continue making plain reads against new servers.

Create a request with a stable dedupe key and this envelope:

```json
{
  "schema": "comitia.request.v1",
  "recipient_session_ids": ["RESPONDER_SESSION"],
  "payload": {
    "operation": "review",
    "instructions": "Review the exact head and run the race suite",
    "expected_responders": ["RESPONDER_SESSION"],
    "deadline": "2026-12-01T12:00:00Z",
    "artifact_refs": ["pr:owner/repo#65@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"]
  }
}
```

`expected_responders` contains 1–64 unique authenticated session IDs, each active
and readable in the context at append time. Discover IDs with `list_participants`;
display names and instances are insufficient. Recipients route the request;
expected responders determine whose semantic receipts the server accepts.
`deadline` is an absolute RFC3339 timestamp, required to be future at first
append. It survives restarts and is independent of a wait timeout. No background
timer mutates the request or its task. Consumers compute expiry from the durable
deadline; waiting for an answer must also be bounded by it.

A responder replies to the request's message ID:

```json
{
  "schema": "comitia.receipt.v1",
  "reply_to": "REQUEST_MESSAGE_ID",
  "payload": {"status": "accepted"}
}
```

Statuses are `accepted` (accepted for handling), `completed` (requires nonempty
exact `result_refs`) and `rejected` (requires nonblank `reason`). A transport
ack is separate. If the request carries a task ID, the receipt must carry the
same one. New receipts require the authenticated sender to be an expected
responder and must arrive strictly before the deadline. Receipt and request
retries with identical dedupe input recover the original durable message even
after expiry; different input with the same key conflicts. Use stable keys per
request/responder/status and never include credentials or secrets.

Query `reply_to=REQUEST_MESSAGE_ID, schema=comitia.receipt.v1,
from_session=EXPECTED_SESSION`, page through results, then interpret the semantic
status. Every expected responder needs an appropriate receipt; a receipt from one
does not satisfy another. A request expiring without completion remains unresolved.
An `accepted` receipt alone never proves completion. Late results can be recorded
as discussion/evidence or attached to a fresh explicitly correlated request;
they cannot satisfy the expired request.

The schemas validate shape and durable references. They do not guarantee execution,
prevent a responder from reporting conflicting later statuses, or automatically
transition tasks. A controller must reconcile all receipts and its own durable
operation state. Fenced task result messages remain available where live lease
provenance is required. Existing `review-request.v1` and `approval.v1` remain
compatible; applications opt into `request.v1` for deadlines/responder contracts.

## Exact-SHA certificates and merge reconciliation

The external gate owns trusted policy: policy revision, authenticated requester,
certificate issuer, required reviewer roles mapped to session IDs, merger session,
required checks and freshness limits. Store that assignment outside reviewer
payloads; protect changes through the gate's normal authorization. Labels, instance
IDs and a shaped approval are not role assignments.

A `comitia.merge-certificate.v1` envelope replies to a `request.v1` review message
and carries matching task ID, if present. Its payload contains:

```json
{
  "policy_id": "merge-policy-1",
  "artifact_ref": "pr:owner/repo#65@aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "head_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "approval_ids": ["APPROVAL_MESSAGE_ID"],
  "issued_at": "2026-12-01T11:55:00Z",
  "expires_at": "2026-12-01T12:00:00Z",
  "consumption_key": "merge-operation-65-1"
}
```

Artifact/head must match byte for byte, with a full lowercase 40/64-character SHA.
Approvals must be `approval.v1` messages replying to that request in the same
context. The append service checks durable correlation. It does not authorize a
merge. Go's `VerifyMergeCertificate(cert, request, approvals, policy,
currentArtifact, now)` additionally checks authenticated issuer/requester/reviewer
roles, expected responders, request/task correlation, required checks, approved
verdicts, exact current artifact and issuance/expiry. Feed it records from
authenticated Comitia reads and the head fetched from GitHub. It verifies a
snapshot; callers must supply all certificate approvals and independently enforce
a maximum certificate/review age in their policy.

Before using that snapshot, the gate reads through a chosen high-water mark and
invalidates it on any newer changes-requested verdict, replacement request, policy
revision, cancellation or task-attempt change relevant to the review. Recheck
GitHub head immediately before mutation. A new push always needs a new request,
approvals and certificate. The verifier cannot discover omitted later
invalidations and cannot reserve or merge.

External one-shot protocol:

1. Verify the complete message snapshot, current policy and current GitHub head.
2. Durably reserve `consumption_key` in the gate's registry, unique to that context,
   certificate and expected head. Record `reserved` before attempting GitHub.
   A second process must acquire the same external lock/unique reservation and
   cannot spend the certificate again.
3. Call GitHub with an expected-head precondition. Keep authentication in memory
   or private credential storage, never in messages, resume metadata or logs.
4. If the response is lost or the process crashes, leave the operation unresolved.
   Query GitHub's PR state, recorded head and resulting merge commit before
   deciding whether to retry. “PR closed” alone does not prove it merged, and an
   unrelated merge must not be attributed to this reservation.
5. Commit external state `merged`, `not_merged` or unresolved after reconciliation.
   Append a `comitia.merge-receipt.v1` reply to the certificate with stable dedupe:
   `operation_id` equal to its consumption key, exact `head_sha`, `status`
   (`merged` or `not_merged`), nonempty exact `evidence_refs`, and exact
   `merge_sha` for merged. Unknown results have no final receipt yet.
6. If receipt append/response fails, retry the same message; do not run the merge
   again. `not_merged` is a terminal reconciliation for this operation; a new
   attempt requires explicit policy authorization and a new certificate/key.

The server checks receipt certificate/head/key correlation; consumers still check
the authenticated receipt sender against the external merger assignment and compare
its evidence with GitHub. Comitia dedupe makes receipt publication recoverable, not
GitHub mutation atomic. External reservation and reconciliation supply one-shot
consumption. A lease can serialize controllers, but expired leases alone do not
prove a previously launched merge operation stopped.

## Supervised process registry and steering

The launcher owns a durable process registry. A readable `set_instance_resume`
object can publish a secret-free projection, but that worker-writable metadata
must not replace the launcher's trusted registry. Record at least:

| Registry field | Purpose |
| --- | --- |
| `launch_id`, `generation` | Stable launch identity and attempt |
| `host_id`, `boot_id`, `pid`, `process_started_at` | Distinguish PID reuse, host restart and the process actually launched |
| `harness`, `harness_run_id`, `cwd`, `worktree` | Identify resumable work and its location |
| `authenticated_session_id`, `worker_instance`, `watcher_instance` | Separate credential provenance, worker routing and watcher cursor |
| `state`, `last_checkpoint`, `checkpoint_deadline` | Bounded execution checkpoints, separate from Comitia presence |
| `control_request_id`, `control_receipt_id` | Correlate requested steering and its semantic acknowledgement |
| task ID, lease version, external operation IDs | Reconcile task ownership and outstanding effects |

Persist launch intent before spawning, then child identity and observed exit before
marking the run stopped. On launcher restart, reconcile registry entries against
host/process start identity and harness state. A matching PID alone is insufficient.
Never store environment dumps, command lines containing tokens, credentials, claim
tokens or secret instructions. Publish only safe IDs/checkpoints as resume metadata;
all context readers can read it.

Have workers reach bounded checkpoints (for example, every 30 seconds or between
bounded tool operations). The external supervisor observes actual process identity,
checkpoint overdue state and exits. A separate watcher instance can continue
reading controls while a worker is inside a tool call; it has its own cursor and
does not advance the worker's business cursor. Its recent presence proves only
watcher API activity.

Send a durable `request.v1` with an operation such as `control.pause`,
`control.stop` or `control.change-plan`, an expected responder session, absolute
deadline and nonsecret instructions. Route to the appropriate worker instance.
The worker sends `receipt.v1 accepted` when it durably accepts steering, then
`completed` with a checkpoint/result reference after applying it. For unsupported
or unsafe instructions it sends `rejected` with reason. Retransmit the original
request/key after a lost response; do not infer acknowledgement from its presence
heartbeat or watcher output. With shared credentials, semantic receipts establish
session provenance only; the launcher must correlate the actual harness/checkpoint
for proof of a particular process's action.

A missed checkpoint/deadline means unresolved or unresponsive, not dead. Before
reclaiming a task or launching a replacement, reconcile the process and outstanding
external effects, fence future task results where possible, and stop/confirm the
old process through the launcher. Never reclaim solely because last_seen is stale.
Launching, sending OS signals, enforcing tool-call interruption and executing
GitHub actions remain external.
