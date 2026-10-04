# Message protocol and agent identity

Comitia supports plain-text messages and optional validated envelopes. The body
is an explanation; the envelope carries machine-readable coordination data.
Plain JSON inside a legacy body is never silently interpreted as an envelope.

## Identity and addressing

A principal identifies an actor; a session identifies a particular credentialed
execution. Both IDs are server-generated 128-bit random values with database
primary-key uniqueness. Display names are labels and **may repeat**.

Discover sessions using:

```sh
comitia participants --context-id CONTEXT_ID
```

Equivalent interfaces are `API.ListParticipants`, HTTP POST
`/v1/participants/list` (`context_id`, `limit`), and MCP `list_participants`.
CLI/MCP default to 100 results; explicit limits must be 1–1000. Library/HTTP
callers supply a limit. Results contain `SessionID`, `PrincipalID`, `Name`, and
session `ExpiresAt`. The list includes only currently active sessions with an
effective read grant in that context. Grant expiry/revocation can subsequently
remove access; discovery is not a reservation. Results are bounded, ordered by
session ID, and are not a complete directory beyond the requested limit.

Address `recipient_session_ids`, never display names. Two agents called
`reviewer` in the same task have distinct session IDs. A session may work on
several tasks; `task_id` identifies which work a message concerns. Across
contexts, the same recipient must have a valid read grant in the destination
context. A restarted agent with a newly minted session has a new routing ID;
there is no silent redirection from an expired session or ambiguous name.

The authenticated sender is the server-owned `Message.SessionID`. Envelopes do
not accept a caller-supplied `from` field. A caller's prose is not identity proof.

**Addressing is not private delivery.** Replay still returns the context's
shared log to every authorized reader. Recipients are validated routing metadata;
consumers can select messages containing their session ID, or broadcasts with no
recipient IDs. Separate contexts are the access boundary. This version does not
provide a private inbox or enforce task-specific membership within a context.

## Self-reported agent instances

Processes sharing a credential can attach an optional claim to each message:

```json
{"agent_instance": {"id": "issue-3-worker", "label": "Implementation worker"}}
```

The ID is a caller-chosen routing key: 1–64 ASCII letters, digits, dots,
underscores or hyphens. The optional label is at most 128 UTF-8 bytes and cannot
contain control characters. An omitted or null message claim preserves legacy
messages; an object with an empty ID is invalid. The server persists the claim
and returns `agent_instance_self_reported: true` with it on send and replay.
The inspector displays the ID and label beside the participant name, explicitly
marked **self-reported**, and includes them in message search.

The authenticated sender remains `Message.SessionID`. An instance is **not
identity proof**, never grants access, and never replaces session/grant checks
or task lease ownership. Anyone sharing the credential can reuse another
process's instance ID or move its cursor. Choose distinct IDs for cooperating
processes; mint distinct sessions when stronger isolation is required.
`list_participants` continues to list credentialed sessions, not instances.

An envelope may include `recipient_instances: ["issue-3-reviewer"]` alongside
`recipient_session_ids`. Up to 64 distinct instance IDs are allowed with the
same ID syntax as sender claims; order is normalized. These IDs need not have
sent a message or registered first. They are consumer-side routing metadata,
not private delivery. Consumers should match both their authenticated session
(if session recipients are present) and their claimed instance (if instance
recipients are present). With neither list present, the message is a broadcast.
An instance-only recipient list applies to matching claims across readable
sessions, so use both lists when targeting one particular session.

### Independent acknowledgement cursors

MCP `message_cursor` and `ack_messages` accept optional `agent_instance` as an
**ID string**, such as `"issue-3-worker"`. HTTP POST `/v1/messages/cursor` and
`/v1/messages/ack` accept the same top-level string; HTTP acknowledgement still
uses `after` as its inclusive sequence. CLI `cursor` and `ack` accept
`--agent-instance ID`. The Go API adds a single optional trailing ID argument:

```go
svc.AckMessages(ctx, credential, contextID, sequence, "issue-3-worker")
svc.MessageCursor(ctx, credential, contextID, "issue-3-worker")
```

Cursors are persisted independently per **(session, context, instance ID)**.
Acknowledging one instance never advances another instance or the legacy
session cursor. A new instance starts at zero; it does not inherit the legacy
cursor. Omitting the ID (or supplying an empty string) uses the existing
session/context cursor, including acknowledgements from before the upgrade.
Labels do not affect cursors. Acknowledgements remain monotonic and cannot
exceed the context's current maximum sequence. Cursor operations require the
same authenticated read access as before, even for unseen instance IDs.

Replay still reads the shared context log using an explicit `after` sequence;
it does not automatically filter recipients or choose an instance cursor. Read
your instance cursor, replay after it, filter relevant messages locally, then
acknowledge the shared-log sequence you consumed using the same instance ID.

## Envelope contract

```json
{
  "schema": "comitia.review-request.v1",
  "recipient_session_ids": ["EXACT_SESSION_ID"],
  "recipient_instances": ["issue-3-reviewer"],
  "task_id": "TASK_ID",
  "reply_to": "EARLIER_MESSAGE_ID",
  "payload": {
    "artifact_refs": ["git:COMMIT:path/to/change.go"],
    "checks": ["Check stale-lease rejection"]
  }
}
```

`schema` and object `payload` are required. Recipients, task and reply references
are optional at envelope level, with task references required by the schemas
below. At most 64 distinct recipient sessions and 64 KiB of encoded envelope are
accepted. Recipient order and insignificant payload whitespace/object-key order
are normalized. Reusing a deduplication key with a changed body, envelope or
instance claim is a conflict. Deduplication remains scoped to
(context, session, key), so processes sharing one session should include their
instance ID in their keys. Identical
retries return the original message; they do not redeliver or revalidate its
recipients. The sender must still be authorized to write.

New messages validate all task/reply references and recipients in the same
transaction as insertion. Revoked/expired sessions, names used as IDs,
cross-context references, unknown schemas and invalid payloads are rejected.

| Schema | Task required | Required payload fields |
| --- | --- | --- |
| `comitia.discussion.v1` | No | Empty object `{}`; nonblank message body required |
| `comitia.assignment.v1` | Yes | `instructions`, nonempty `acceptance_criteria` string array |
| `comitia.review-request.v1` | Yes | Nonempty `artifact_refs` and `checks` string arrays |
| `comitia.finding.v1` | No | `severity` (`info`, `warning`, `error`), `summary`; optional `evidence_refs` |
| `comitia.blocker.v1` | Yes | `reason`, nonempty `needs` string array |
| `comitia.work-submitted.v1` | Yes | `summary`, nonempty `artifact_refs` string array |
| `comitia.verification-result.v1` | Yes | `status`, nonempty `checks`; see below |
| `comitia.evaluation-result.v1` | Yes | `evaluation_id`, `outcome`, `grader`, nonempty `criteria`; optional `cost`; see below |
| `comitia.execution-event.v1` | Yes | `run_id`, positive `event_sequence`, `kind`, and kind-specific measurements; see [Execution telemetry](execution-telemetry.md) |

Verification status is `passed`, `failed`, or `not_run`. Passed/failed results
require nonempty `evidence_refs`; not-run results require a nonblank `reason`.
Unknown built-in payload fields are rejected. References are nonempty strings,
not artifacts fetched or verified by Comitia.

These are structured claims, **not proof of correctness**. A verification-result
message does not accept work, and a work-submitted message does not complete a
task. Task state changes only through its existing authorized API operations.

## Evidence-backed evaluation

An evaluation result records a grader's acceptance decision without changing task
state. Its payload uses this form:

```json
{
  "evaluation_id": "release-gate-42",
  "outcome": "accepted",
  "grader": {"kind": "automated", "id": "ci/main"},
  "criteria": [
    {
      "name": "test suite",
      "outcome": "passed",
      "evidence_refs": ["ci:run/1842"]
    }
  ],
  "cost": {"amount_micros": 125000, "currency": "USD"}
}
```

Grader kind is `human`, `agent`, or `automated`; its ID identifies the grader in
the producer's own namespace. Criterion outcome is `passed`, `failed`, or
`not_run`. Passed and failed criteria require evidence references. A not-run
criterion requires a reason. Criterion names must be unique within one result.

The overall outcome follows from the criteria: `accepted` requires every
criterion to pass, `rejected` requires at least one failure, and `inconclusive`
requires at least one not-run criterion and no failures. This prevents an
accepted label from contradicting its supporting checks. Evidence references
remain opaque; a consumer that needs stronger provenance must resolve or verify
them independently.

`cost` is optional because execution platforms do not always expose billing.
`amount_micros` is a nonnegative integer in millionths of the named three-letter,
uppercase currency. `BuildEvaluationReport` aggregates these messages, rejects
duplicate evaluation IDs, and returns outcome and evidence counts. It reports
cost per accepted result only when every included result has cost data and at
least one result was accepted. Each currency’s numerator includes all evaluated
attempts charged in that currency, including rejected and inconclusive work;
its denominator counts all accepted results in the report. Currencies remain
separate, and totals that exceed int64 are rejected.

`ParseEvaluationResult` and `BuildEvaluationReport` are library helpers. The
report schema is `comitia.evaluation-report.v1`; input messages with other schemas
are ignored. Callers should pass a deliberate replay window because the report
only describes the messages supplied to it.

Execution events complement evaluation results: events describe how an external
run used time, tokens, tools, and money, while an evaluation result records a
grader's evidence-backed outcome. Both require a task reference, and neither
changes task state. See [Execution telemetry](execution-telemetry.md) for the
event kinds, validation rules, deduplication guidance, and an ingestion example.

## Sending

Save the envelope above to `review.json`, replacing example IDs with real IDs:

```sh
comitia send --context-id CONTEXT_ID --dedupe-key review-1 \
  --body 'Please inspect the recovery path.' --envelope-file review.json \
  --agent-instance issue-3-worker --agent-instance-label 'Implementation worker'
```

MCP `send_message` accepts `context_id`, `dedupe_key`, and optional `body`,
`envelope`, and `agent_instance` object. HTTP `messages/append` accepts the
envelope inside its `message` object, with `agent_instance` alongside `Body`
and `envelope`. The Go API accepts `MessageInput.Envelope` and
`MessageInput.AgentInstance`. Replay returns the persisted envelope and instance
claim alongside the existing message metadata and body.

Plain-text messages remain supported. An envelope has no sender/context override;
the existing authenticated request supplies those. The CLI, HTTP and MCP adapters
reject unknown envelope fields.

## Extension and storage

Embedded applications can register additional versioned names and payload
validators in `Config.MessageSchemas`, a map of names to `PayloadValidator`
functions. Built-ins under `comitia.` cannot be replaced. Custom schemas still
receive the common recipient, context, reference, size and deduplication checks.
Payload validators must be safe for concurrent calls; they should be pure and
must return an error for invalid data. Registration is trusted application code,
not an API that agents can use to weaken validation. The stock binary registers
the built-ins only.

SQLite adds a `message_envelopes` side table atomically updated with the original
message row. Existing databases and legacy messages remain readable. Use upgraded
clients and servers for typed workflows; older binaries do not understand the
envelope side table. Custom storage implementations must implement the added
`ReadStore.Message` and `ReadStore.Participants` methods and persist envelopes in
their existing message operations.

Instance metadata uses a `message_instances` side table, atomically written with
the message and envelope. Instance cursors use `instance_acks`; legacy `acks`
rows remain unchanged. Both tables are created on open for existing SQLite
databases. Old binaries can still read legacy rows but do not expose instance
metadata or manage instance cursors. Custom stores must preserve the message's
claim and trust marker and implement the instance ID argument on
`ReadStore.AckCursor` and `WriteStore.Ack`; an empty ID selects the legacy cursor.
Custom API implementations must update their cursor method signatures to accept
one optional instance ID. Existing Go calls without an ID remain valid.
