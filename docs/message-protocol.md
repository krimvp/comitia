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
CLI/MCP/HTTP default omitted limits to 100; explicit limits must be 1–1000. Library
callers supply a limit. CLI/MCP results contain `SessionID`, `PrincipalID`, `Name`, and
session `ExpiresAt`; canonical REST responses use `session_id`, `principal_id`, `name`, and `expires_at`.
Unnegotiated `/v1/` responses retain legacy record names; see the
[wire compatibility policy](local-daemon.md#rest-wire-compatibility). The list includes only currently active sessions with an
effective read grant in that context. Grant expiry/revocation can subsequently
remove access; discovery is not a reservation. Results are bounded, ordered by
session ID. Use `participants --page` or MCP `page:true` for
`{participants,next_after,has_more}`, then pass `next_after` as `--after-id`
or `after_id` until `has_more` is false. The cursor is the last returned session
ID and is exclusive; a nonempty cursor implies page output. Library callers
use `ListParticipantPage(ctx,api,...)`, which detects optional
`ParticipantPageAPI`; HTTP accepts `page` and `after_id` on the same route
and returns `participant_page`. Legacy calls still return arrays. Each page
rechecks current session and effective-grant eligibility, so expired or revoked
candidates cannot truncate a page while later readable sessions remain.
Custom stores may implement optional `ParticipantPageStore` with
`ParticipantsAfter(contextID,now,sessionID,limit)`, the same effective-grant/session
filtering and an exclusive ID bound. Existing API/store implementations still
compile and ordinary list calls are unchanged. Without paging capabilities,
the helper and service page the existing list only when a 1,000-entry read
returns fewer than 1,000 participants; reaching that ceiling returns an explicit
unsupported error because the legacy list cannot establish whether a tail remains.

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

### Process defaults and overrides

Set `COMITIA_AGENT_INSTANCE` and optional `COMITIA_AGENT_INSTANCE_LABEL` in the
environment passed to Comitia, not merely in the model prompt.

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
cursor. The CLI, MCP adapters, and Go HTTP client use `COMITIA_AGENT_INSTANCE`
when the ID is omitted. Without that default, omitting the ID uses the existing
session/context cursor, including acknowledgements from before the upgrade.
An explicit empty string selects that legacy cursor even with a default set.
Labels do not affect cursors. Acknowledgements remain monotonic and cannot
exceed the context's current maximum sequence. Cursor operations require the
same authenticated read access as before, even for unseen instance IDs.

Replay still reads the shared context log using an explicit `after` sequence;
it does not automatically filter recipients or choose an instance cursor. Read
your instance cursor, replay or wait after it with optional filters, then
acknowledge the last matching sequence you consumed using the same instance ID.
Session recipient checks can still be applied locally when needed.

## Filtered replay and waiting

MCP `replay_messages` and `wait_messages`, HTTP POST `/v1/messages/replay` and
`/v1/messages/wait`, and CLI `replay`, `wait`, and `watch` accept these optional
filters. Wire fields are top-level; CLI flags replace underscores with hyphens.
Empty values disable a filter; all nonempty filters must match.

| Field | Match |
| --- | --- |
| `for_instance` | Envelope `recipient_instances` contains this exact ID; or instance recipients are absent/empty and `recipient_session_ids` contains the caller's authenticated session; or both recipient lists are absent/empty (including legacy messages without an envelope). Session-addressed messages without instance recipients reach every instance of that session. |
| `strict_recipients` | Opt-in boolean, default `false`; requires `for_instance`. Every nonempty recipient list must match: session recipients must contain the authenticated caller session and instance recipients must contain `for_instance`. Empty lists are unrestricted; broadcasts and legacy messages still match. |
| `reply_to` | Direct replies to a message in this readable context; missing/foreign roots are forbidden. |
| `thread_id` | Root message and recursive reply descendants in this readable context. |
| `from_session` | Exact authenticated sender session ID. |
| `from_instance` | The message's self-reported `agent_instance.id` equals this exact ID. Legacy senders without a claim do not match. |
| `task_id` | Envelope `task_id` equals this exact ID. |
| `schema` | Envelope `schema` equals this exact versioned name. |

Instance filters use the same 1–64 character ID syntax as sender claims.
`agent_instance` (an ID string, defaulting to `COMITIA_AGENT_INSTANCE`) is not a
filter: it names the reader for [instance presence](presence.md).
Unknown task IDs and schema names simply match nothing. These filters are
convenience selectors over the same shared context log. They neither grant
access nor restrict what an authorized reader can obtain with another replay.
`for_instance` uses the authenticated caller session for session-only messages.
If instance recipients are present, the exact instance match takes precedence;
a consumer targeting one session can set `strict_recipients: true` (CLI
`--strict-recipients`) to apply both lists. A strict replay is still a convenience
selector over the context log; another authorized reader can replay without it.
The HTTP Client sends `strict_recipients` only when it is set, so ordinary reads
still work against older servers. A strict read against a server that rejects
the member returns `ErrInvalid` ("strict_recipients unsupported by this
server"); the Client never retries without it, because that would widen results.

Replay keeps its existing output, ordering, exclusive `after` cursor and
1–1000 `limit`. Filtering happens **in SQL before ORDER BY/LIMIT**, so a page
contains up to `limit` matching messages regardless of skipped sequences.
Advance `after` to the last returned message's `Sequence`; an empty result means
there are currently no later matches. Skipped sequences need no separate scan
cursor. The default output shape, including empty-result serialization, is unchanged.

```go
filters := comitia.MessageFilters{ForInstance: "issue-3-worker", StrictRecipients: true}
messages, err := svc.ReplayMessages(ctx, credential, contextID, after, 100, filters)
messages, err = svc.WaitMessages(ctx, credential, contextID, after, 30*time.Second, filters)
```

Wait returns immediately if a matching message already exists, otherwise as
soon as one is appended, with up to 100 matches in sequence order. A timeout
returns an empty list without error. Each call caps the timeout at **60 seconds**,
including MCP, HTTP and Go; negative values are invalid. Zero performs an
immediate read. MCP, HTTP and CLI default to 30 seconds. HTTP/MCP
`timeout_seconds` is a number; omitted/null uses the default, and explicit zero
performs one initial authorized read immediately. Positive timeouts apply to
scan admission, SQL queries and optional presence writes as well as waiting;
matches already read survive an optional heartbeat timeout. Caller cancellation
returns an error. Fractional seconds are supported. Set client/proxy deadlines
above the requested timeout; the stock HTTP client's default is 70 seconds. A client with a shorter deadline
should request a shorter wait and repeat it after each empty result.

```json
{"context_id":"CONTEXT_ID","after":42,"timeout_seconds":30,"for_instance":"issue-3-worker","schema":"comitia.discussion.v1"}
```

Replay and wait bound the **encoded JSON messages array** to 1 MiB by default,
including brackets, commas, metadata, envelopes and JSON escaping. Optional
`max_bytes` (`--max-bytes` on CLI, `MessageFilters.MaxBytes` in Go) accepts
0–4194304; zero selects the 1048576-byte default. The count limit still applies:
replay accepts 1–1000 and wait returns up to 100. Filtering happens before both
limits. The first matching message is always returned intact, even when it
exceeds the soft byte budget, so readers can advance without losing its body.

Default MCP/CLI results remain arrays. To request continuation metadata, pass
`page: true` to replay/wait tools or `--page` to CLI `replay`/`wait`:

```json
{"messages":[],"next_after":42,"scanned_through":57,"has_more":false}
```

`next_after` is the last returned sequence, or the supplied `after` on an empty
page. `scanned_through` reports safe progress through skipped nonmatches from
that same read snapshot; an empty filtered page can advance it beyond
`next_after`. After handling returned messages, continue with `scanned_through`
and exactly the same filters to avoid scanning the same tail again. Count and
byte truncation never advance it past an omitted match. Changing filters means
returning to the appropriate earlier cursor. Custom APIs/stores without scan
tracking conservatively report only `next_after` as `scanned_through`. In legacy
arrays, use the last message's `Sequence`. Reading and continuing never
acknowledge messages: acknowledge only after handling them. `has_more` comes
from a separate authorized lookahead and is advisory; later appends can arrive
after it returns false. Positive page waits share their timeout with the
lookahead. If only that lookahead times out, already-read messages survive and
`has_more` is conservatively true; timeout zero keeps its immediate read. An
empty page always has `messages: []`.

REST requests accept the same `max_bytes` and `page` fields. With `page: true`,
the response is `{"page":{"messages":[],"next_after":42,"scanned_through":57,"has_more":false}}`.
Go callers can use `ReplayMessagePage`/`WaitMessagePage`, or the optional
`MessagePageAPI` implemented by Service and the HTTP client. Existing custom
APIs need no new methods; the helpers derive pages using their legacy reads.

MCP replay/wait also accept `compact: true`: `content.text` becomes a short
count/cursor summary while `structuredContent.result` retains the full array
or page. By default both fields carry the full result for compatibility.
Check the loaded tool schema before using new fields with an older adapter.
REST requests remain capped at 1 MiB; the Go remote client independently caps
responses at 16 MiB and reports an explicit oversize error rather than a
truncated-JSON decode failure. Smaller byte/count limits recover aggregate
pages; a single embedded message exceeding the response cap requires an
embedded reader. The client also rejects trailing response data.

HTTP replay and wait always include `messages`, using `{"messages":[]}` for an empty
result (or `page.messages: []` in page mode). The Go HTTP client requires that
collection member in negotiated canonical responses, but normalizes missing/null
empty collections from older unnegotiated Handlers. MCP and CLI return an empty JSON array; the inspector remains
tolerant of older servers that omitted empty collections.

Append broadcasts an in-process notification **after commit** to every waiter
in that context. Wait subscribes before reading to prevent a lost wakeup. A
200 ms fallback poll detects commits by other services/processes using the same
SQLite database and rechecks session/read-grant validity. No database transaction
or connection is held while waiting. Context cancellation interrupts the wait;
service/HTTP errors preserve the existing authentication, authorization and
invalid-input sentinels. Deduplicated retries do not notify because they append
no new row.

SQLite indexes context/sequence projections for schema, task and sender, plus
recipient membership, so sparse filtered reads seek candidates before loading
message bodies or decoding envelopes. Waits retain a scanned-through sequence;
after an empty scan, later notifications and 200 ms polls evaluate only newly
appended rows. Page results export that progress for subsequent calls. Scanning
never changes session or instance acknowledgement cursors.
Polling remains enabled to detect other processes writing the SQLite file.
Custom stores without scan tracking retain replay from the original `after`.

**Stdio MCP concurrency:** up to eight tool calls run concurrently on one
connection. A pending `wait_messages` leaves ping, tool listing and other tool
calls responsive. Further tool calls receive JSON-RPC error `-32000` while all
eight slots are occupied; retry after a call completes. Responses can arrive in
any order and must be matched by request ID. Stdout contains one complete JSON
message per line. Calls supplied together in a pipe can execute and complete
out of order: for example, a replay may run before an earlier send commits.
For dependent operations, keep stdin open and await one response before sending
the next request.

Send `notifications/cancelled` with `params.requestId` to cancel a pending tool
call. The request context is interrupted and no response is sent for that call;
unknown, completed or malformed cancellation notifications are ignored. A
response already sent can race with cancellation. Clients cannot cancel
`initialize`. On input EOF the server allows one second for calls to finish,
then cancels remaining calls and waits up to one more second. If EOF cancels
calls, Serve returns an error naming the count and the CLI exits nonzero, since
those calls have no responses. Server context cancellation interrupts calls
immediately, with up to one second to stop. MCP's
default wait remains 30 seconds, consistent with HTTP/CLI. Both replay and wait
reject fields belonging only to the other tool (`timeout_seconds` on replay and
`limit` on wait).

### Watching from the CLI

```sh
comitia watch --context-id CONTEXT_ID --agent-instance issue-3-worker \
  --for-instance issue-3-worker --schema comitia.discussion.v1
# Through the installed wrapper:
comitia-client codex watch --context-id CONTEXT_ID \
  --agent-instance issue-3-worker --for-instance issue-3-worker
```

Watch reads the specified instance's persisted acknowledgement cursor on
startup. The `--agent-instance` flag selects the cursor and defaults to
`COMITIA_AGENT_INSTANCE`; without a default, or with an explicit empty
`--agent-instance=`, watch uses the legacy session cursor. `--for-instance`
selects recipients and is never defaulted.
It then repeatedly waits and prints tab-separated lines:

```text
43	issue-3-reviewer	comitia.discussion.v1	First line of the message body
```

Missing sender instance or schema is printed as `-`. Only the first body line
is printed (a trailing CR is dropped). Sender-controlled fields (sender
instance, schema and body preview) are escaped so each message is exactly one
physical line and cannot drive the terminal: backslash becomes `\\`, invalid
UTF-8 bytes become `\xNN`, and every non-graphic character (C0 controls
including TAB, CR, ESC and BEL, DEL, C1 controls, Unicode format/bidi and
line/paragraph separators) uses its Go escape, such as `\t`, `\r`, `\a`,
`\x1b` or `\u202e`. Raw tabs only separate fields. Use `replay` for the exact
JSON body, or `watch --json-lines --manual-ack` for complete records. The
[durable consumer recipe](durable-coordination.md#process-then-ack) commits business
effects before a separate ack; terminal display alone does not prove handling.
Every successful default terminal print is followed by a monotonic acknowledgement
using the same instance. `--no-ack` disables these writes while retaining the
in-memory sequence and resuming from the persisted cursor on startup. Filtered
watch therefore consumes a particular view of the log: use a separate instance
cursor for a different view if you need to revisit skipped messages.

`--timeout-seconds` defaults to 30, caps at 60, and must be positive for watch.
Watch always starts from the persisted cursor; `--after` is for replay/wait.
Connection/server failures retry with backoff from 250 ms to 5 seconds. The
in-memory sequence survives reconnects; failed acknowledgements are retried
before reading more, without reprinting the same line. Authentication,
authorization and invalid-input errors end watch. Ctrl-C/SIGTERM cancels it.
If the watch process itself dies after printing but before acknowledgement,
that line can appear again when it restarts; delivery remains at least once.

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
are normalized. Deduplication is scoped to (context, authenticated session,
instance ID, key); an omitted instance has its own legacy scope. Reusing a key
within that scope with a changed body, envelope, or instance label is a conflict.
Changing the instance ID, including changing between nil and a set instance,
appends a new message. A process restarted with `COMITIA_AGENT_INSTANCE` set can
therefore duplicate a retry originally sent without an instance. Processes with
distinct instances can reuse natural keys without instance prefixes. Identical
retries return the original message; they do not redeliver or revalidate its
recipients. The sender must still be authorized to write.

With an environment default configured, send cannot opt out of an instance:
an explicit empty send ID is invalid. Cursor/ack accept an explicit empty ID
(`agent_instance: ""` or `--agent-instance ""`) to select the legacy cursor;
on replay/wait it records no presence activity.

New messages validate all task/reply references and recipients in the same
transaction as insertion. Revoked/expired sessions, names used as IDs,
cross-context references, unknown schemas and invalid payloads are rejected.

Runtime discovery returns all registered names, including configured extensions:
MCP `list_message_schemas` takes `{}` and `get_message_schema` takes
`{"schema":"comitia.review-request.v1"}`. HTTP POST
`/v1/messages/schemas/list` takes `{}` and `/v1/messages/schemas/get` takes the
same schema argument, returning `message_schemas` and `message_schema`,
respectively. The optional Go `MessageSchemaAPI` supplies `ListMessageSchemas`
and `GetMessageSchema`. Both require a valid session, independent of context
grants; descriptors contain contracts and illustrative IDs, never context data.
Unknown IDs return `ErrNotFound`; adapters whose API lacks the optional
capability return an explicit unsupported error. List order is lexicographic.
Descriptor fields use the same snake_case names in legacy and canonical HTTP
responses. Against a server without these routes, the HTTP Client returns
`ErrInvalid` ("message schema discovery unsupported by this server") instead of
`ErrNotFound`, so a missing route is never mistaken for an unknown schema.

Each descriptor includes `id`, a draft 2020-12 `payload_schema` for built-ins,
`body_required`, `task_required`, `task_or_artifact_refs`, semantic
`requirements`, and `examples` with body/envelope pairs. `server_authored: true`
marks schemas that replay can return but appends reject. JSON Schema describes
payload shape; requirements describe additional checks such as exact artifact
references, byte limits and conditional evidence. The registered validator is
authoritative: JSON Schema may be looser for documented semantic checks, such as
UTF-8 byte lengths, RFC3339 parsing and uniqueness after trimming criterion
names. Go integer fields also reject decimal/exponent JSON lexemes such as `1.0`
even though JSON Schema calls them integers. Supported optional null values
are included in the schemas. Replace example task IDs with real tasks in the destination
context before sending. Built-in IDs and their JSON Schema `$id` URNs include
the version; discovery reflects the running service's registry.

| Schema | Task required | Required payload fields |
| --- | --- | --- |
| `comitia.discussion.v1` | No | Empty object `{}`; nonblank message body required |
| `comitia.assignment.v1` | Yes | `instructions`, nonempty `acceptance_criteria` string array |
| `comitia.review-request.v1` | Yes | Nonempty `artifact_refs` and `checks` string arrays |
| `comitia.finding.v1` | No | `severity` (`info`, `warning`, `error`), `summary`; optional `evidence_refs` |
| `comitia.blocker.v1` | Yes | `reason`, nonempty `needs` string array |
| `comitia.work-submitted.v1` | No | `summary`, nonempty `artifact_refs` string array |
| `comitia.verification-result.v1` | Unless `artifact_refs` | `status`, nonempty `checks`; optional nonempty `artifact_refs`; see below |
| `comitia.approval.v1` | No | Exact `artifact_refs`, `verdict`, nonempty `checks`, `blocking_count`; see [Approvals](#approvals-and-merge-gates) |
| `comitia.evaluation-result.v1` | Yes | `evaluation_id`, `outcome`, `grader`, nonempty `criteria`; optional `cost`; see below |
| `comitia.execution-event.v1` | Yes | `run_id`, positive `event_sequence`, `kind`, and kind-specific measurements; see [Execution telemetry](execution-telemetry.md) |
| `comitia.request.v1` | No | Durable expected responder sessions, operation/instructions and absolute deadline; optional exact artifacts |
| `comitia.receipt.v1` | Matches request | accepted/completed/rejected semantic receipt replying to a request |
| `comitia.merge-certificate.v1` | Matches request | Exact PR head, approval IDs, policy/freshness and one-shot consumption key |
| `comitia.merge-receipt.v1` | Matches certificate | External reconciled merge outcome, exact head/merge SHA and evidence |
| `comitia.task-status.v1` | Yes | `state`, `summary`; optional `child_context_id`, `blockers`, `links`; see [Task status](#task-status) |
| `comitia.task-event.v1` | Server-authored | Not appendable; replayed `kind`, `status`, optional `lease_version`; see [Authoritative task events](#authoritative-task-events) |

Verification status is `passed`, `failed`, or `not_run`. Passed/failed results
require nonempty `evidence_refs`; not-run results require a nonblank `reason`.
Payload keys are case-sensitive; mis-cased keys are rejected since PR #89
(issue #64), including nested fields such as evaluation `grader.id` and
`criteria[].name`. This deliberately tightens validation of new appends;
duplicate keys at every object level are also rejected for built-in appends.
Previously stored messages are neither rewritten nor revalidated during replay.
Evaluation parsers and reports retain historical case-insensitive decoding and
duplicate-field merging, with their existing semantic validation.
Unknown built-in payload fields are rejected. References are nonempty strings,
not artifacts fetched or verified by Comitia.

Work-submitted and verification-result messages identify their work by
`task_id` or by `artifact_refs`; a verification result with neither is
rejected. A given `task_id` must still name a task in the same context. Without
a `task_id`, every artifact ref must be exact by the same rule as approvals
(below): `main` or `git:abcdef1` is refused, `git:<full sha>` or an opaque
lowercase `scheme:value` such as `build:1842` is accepted. With a `task_id`,
refs stay free-form nonempty strings. A task-backed verification may omit
`artifact_refs` or set it to `null`; a supplied array must be nonempty and contain
nonblank strings. Taskless verification still requires exact artifact refs.

These are structured claims, **not proof of correctness**. A verification-result
message does not accept work, and a work-submitted message does not complete a
task. Task state changes only through its existing authorized API operations.

## Approvals and merge gates

See [durable requests and exact-SHA certificate reconciliation](durable-coordination.md)
for authenticated role assignments, scoped lookup, deadlines, one-shot reservations
and merge receipts. GitHub execution remains external.

`comitia.approval.v1` records a reviewer's verdict on exact artifacts:

```json
{
  "schema": "comitia.approval.v1",
  "recipient_instances": ["merge-agent"],
  "payload": {
    "artifact_refs": ["pr:krimvp/comitia#41@4f2c9e1d0b7a5c3e8f6a1b2d4c6e8f0a1b3c5d7e"],
    "verdict": "approved",
    "checks": ["go test -race ./...", "code review"],
    "blocking_count": 0,
    "summary": "No blocking findings."
  }
}
```

Every artifact ref is `scheme:value` without whitespace, and the scheme is
lowercase (letters, digits, `+`, `-`, `.`, starting with a letter), so `GIT:` or
`Pr:` is refused rather than treated as opaque. `git:` refs must be a full
commit: 40 (SHA-1) or 64 (SHA-256) lowercase hex digits. `pr:` refs must be
exactly `pr:<owner>/<repo>#<positive number>@<full commit>`, with one `#`, one
`@` and non-empty owner and repo. Abbreviated SHAs and branch names are refused,
so an approval never silently carries over to a later push or matches a prefix.
Other lowercase schemes (for example `sha256:`) are accepted as opaque exact
identifiers. `verdict` is `approved` or `changes-requested`;
`blocking_count` is a required integer that must be `0` for `approved` and at
least `1` for `changes-requested`. `summary` is optional. No task is required.

A merge agent can gate on the envelope instead of parsing prose:

1. Replay the context and keep messages whose `Envelope.Schema` is
   `comitia.approval.v1`, whose authenticated `SessionID` is a reviewer you
   trust, and (with a shared credential) whose self-reported `agent_instance`
   is the reviewer instance you assigned.
2. Require `verdict: "approved"` with `blocking_count: 0` and an artifact ref
   that is byte-for-byte the full head commit you are about to merge, e.g.
   `pr:owner/repo#41@<full head sha>`.
3. Treat any later `changes-requested` from the same reviewer for that commit as
   overriding, and any new push as needing a new approval.

The server validates the shape only. Which sessions count as reviewers, and
whether the referenced commit is really the one being merged, remain the merge
agent's decisions.

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

## Task status

A coordinator working in a private child context reports progress to the parent
(for example an epic) context itself; Comitia never relays messages between
contexts. `comitia.task-status.v1` gives those reports a shared form. It is sent
in the parent context, and `task_id` names the parent's task:

```json
{
  "schema": "comitia.task-status.v1",
  "task_id": "PARENT_TASK_ID",
  "payload": {
    "state": "blocked",
    "summary": "Store and API done; waiting on the schema decision.",
    "child_context_id": "CHILD_CONTEXT_ID",
    "blockers": ["Decide whether approvals need a task"],
    "links": ["https://github.com/krimvp/comitia/pull/22"]
  }
}
```

`state` is `planned`, `in_progress`, `blocked`, `review`, `done`, `failed`, or `cancelled`. `summary`
is nonblank and at most 4096 bytes. `blockers` and `links` are optional lists of
at most 64 nonblank strings of at most 1024 bytes each; links are opaque and not
fetched. `child_context_id`, when set, must be a direct child of the context the
message is sent in, so readers know where the detail lives; a grandchild, a
sibling or an unknown ID is rejected as invalid. A child with a nonempty
`TaskID` must serve the same task as the envelope; taskless shared-team children
remain valid. Naming a child requires read
on the current context as well as write, so a write-only sender cannot probe
which contexts are children. The sender does not need read access to the child, and readers still need their own grant to open it. Like the
other schemas, a status is a claim and does not change the task's state.

The inspector shows status messages with their state and task title, and links
the child context when it is among the viewer's loaded readable contexts.

## Authoritative task events

`comitia.task-event.v1` is reserved for the server. Task mutations append these
messages atomically with state changes. The envelope names `task_id`; payload
members are `kind` (`created`, `claimed`, `renewed`, `released`, `completed`,
`failed`, `cancelled`), `status`, `lease_version`, and optional
`previous_status`, `lease_owner_session_id`, `lease_owner_instance`, `reason`.
The message's authenticated `SessionID` identifies the actor whose task
mutation was authorized. These events are broadcast in the task's context and
carry no claim tokens or hashes. Readers can replay/wait/filter them normally;
`send`/`send_message`/`messages/append` cannot author them. Task-status and
execution telemetry remain advisory. Schema discovery lists
`comitia.task-event.v1` with `server_authored: true` and a payload schema
describing replayed events; its examples are illustrative and not appendable. See [task outcomes](task-validation.md#authoritative-events-and-terminal-outcomes)
for failure/cancellation authority, reasons and recovery.

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
functions. Add matching entries in `Config.MessageSchemaDescriptors` to expose
extension payload JSON Schemas, requirements and examples. Descriptor IDs default
to their map keys; supplied IDs must match. Descriptors must describe an object,
be self-contained (external references are not loaded), and provide at least one
example accepted by both JSON Schema and the registered validator. Declared
body/task requirements also apply to sends. New copies descriptor data; discovery
returns copies, so later caller mutations cannot change the registry. Legacy
validator-only registrations remain supported and discoverable with `opaque: true`,
a generic object schema and no examples, since arbitrary validator functions
cannot be reverse-engineered into field contracts. Supply descriptors for complete
extension discovery. Built-ins under `comitia.` cannot be replaced. Custom schemas still
receive the common recipient, context, reference, size and deduplication checks.
Payload validators must be safe for concurrent calls; they should be pure and
must return an error for invalid data. Custom validators retain their own key
matching rules, including those using the historical `strictPayload` decoder.
The canonical-key and duplicate-key append checks apply only to built-ins;
dictionary keys remain caller data. Registration is
trusted application code, not an API that agents can use to weaken validation. The stock binary registers
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

Instance dedupe scopes use the additive `message_dedupe_scopes` table, backfilled
on open for messages written by older binaries. Natural keys remain in
`messages.dedupe_key` whenever (context, session, key) is free, preserving old
readers and retries across downgrade/re-upgrade. A collision between instances
uses an opaque storage key; upgraded readers expose the natural key from the
scope table. Older binaries cannot reproduce instance-scoped dedupe for these
colliding keys and may expose the opaque key or reject the retry. Backfill
conflicts fail migration rather than silently leaving a message without a scope.

Custom API/store implementations must accept optional `MessageFilters` in
`ReplayMessages`/`Messages` and implement `API.WaitMessages`. Existing Go replay
calls without filters remain valid. The `Messages` store method additionally
receives the authenticated caller session ID after the context ID, so it can
match session-addressed messages. When `StrictRecipients` is true it must also
match session recipients for instance-addressed messages; empty lists impose no
condition.
A custom store must filter before limiting (or provide equivalent paging) while
the service retains the read-grant check.
A `ReadStore` may additionally implement the exported optional `MessageScanStore`:

```go
MessagesScanned(contextID, callerSessionID string, after int64, limit int, filters comitia.MessageFilters) ([]comitia.Message, int64, error)
```

The returned scan watermark must be at least `after`. Every match after `after`
through that watermark must be returned, subject to the same count/byte limits;
a limited result must not skip omitted matches. Both results and watermark must
come from the same `Store.View` snapshot, so concurrent appends are not skipped.
It is scan progress for the supplied filters, never proof that skipped messages
were processed. The service retains authorization and presence checks, bounds
returned bytes, and uses `Messages` when the capability is absent. SQLite adds
filter and recipient side tables. SQLite insert triggers on messages, envelopes
and sender instances maintain them in the writer's transaction, including when
an older CLI/binary appends while a new reader is already running. Inserting an
envelope replaces provisional broadcast membership with its final routing;
instance recipients retain precedence over session recipients. Startup backfills
rebuild projections from the base tables when the schema generation changes or
an object is missing, repairing messages that predate the triggers and partial
projections from a missing trigger. Unchanged generations with complete schema
objects keep the lock-free migration probe.

## Task claims by instance

`ClaimTask` accepts one optional `AgentInstance`; MCP `claim_task` and CLI
`task-claim` take it as `agent_instance` / `--agent-instance` (plus
`--agent-instance-label`), and HTTP `tasks/claim` as `claim_instance`. Tasks then
report `lease_owner_instance` from claim, renew, get and list until the task is
claimed again or released; a completed task keeps its last holder. For claim,
renewal and completion result shapes and replacing the rotated token/version
pair, follow the [complete lease loop](task-validation.md#complete-lease-loop). Like message
instances, it is self-reported routing metadata: the lease belongs to
`LeaseOwnerSessionID` and the claim token, not to the instance.

`OpenLease` (MCP and `renew_task` `open_ended: true`, CLI `--open-ended`) asks
for a lease that lasts until the task is completed or released. Every lease is
capped at the holder session's expiry, so an open lease is still bounded. This is
not reserved for orchestrators: any client with `claim` on the context can hold a
lease up to its own session's expiry, bypassing the 86400-second
`lease_seconds` limit of MCP. A context admin can release such a lease. A lease
whose holder session is revoked or expired, or whose holder no longer has
`claim` on the context, counts as expired, so another session can claim the task
at once.
`ReleaseTask` (MCP `release_task`, HTTP `tasks/release`, CLI `task-release`)
returns a leased task to pending and bumps `lease_version`, so the old token goes
stale. The holder releases with its claim token; a session with `admin` on the
context may release without it but must pass the current `lease_version`
(otherwise `stale task claim`). Releasing a pending or completed task is a
conflict. The instance is stored in a `task_instances` side table created on
open; custom stores must persist `Task.LeaseOwnerInstance` in `PutTask`.

Owner results may include `task_fence: {"task_id":"TASK_ID",
"claim_token":"CURRENT_TOKEN","lease_version":2}` on MCP `send_message` or
HTTP `message`. The task ID must match `envelope.task_id`. CLI `send` uses
`--claim-token` and `--lease-version` with `--envelope-file`; Go uses
`MessageInput.TaskFence`. Append checks the authenticated holder, claim permission,
current token/version, status and unexpired lease in the same write transaction.
Stale workers cannot append new fenced results after renewal, release, reassignment
or completion. The returned/replayed `task_attempt` contains only the validated
task ID and lease version; the token is never published. Consumers should match
`Message.SessionID`, `envelope.reply_to` (the intended request) and `task_attempt`
against the expected owner and attempt. A fence proves ownership when appended;
previously accepted history remains readable after reassignment. An identical
dedupe retry returns that accepted message after token rotation, with current
write/claim permission still required; changing or removing the fence conflicts.
Unfenced observer reviews remain supported and carry no owner-attempt proof.
The fence covers Comitia append, not external side effects. SQLite stores the
attempt and a private token hash in a side table; custom stores must persist
`Message.TaskAttempt`, including its non-JSON hash, for conflict-checked retries.
HTTP returns `task_attempt` with the same snake_case members in legacy and
canonical responses. Servers without the `task_result_fencing` feature reject
`task_fence` while decoding, so nothing is appended; the Go HTTP client returns
`httpapi.ErrTaskResultFencingUnsupported` instead of retrying unfenced.
Task create/claim/renew optionally use session-scoped non-secret mutation receipts.
An exact current-lease claim/renew retry issues a fresh token at the same version
and expiry, invalidating earlier tokens; receipts for moved-on leases conflict.
See [the complete lease loop](task-validation.md#complete-lease-loop).

## Validation details and capability discovery

Validation errors keep the `invalid input` sentinel and name the rejected field
or rule, for example `invalid input: task_id required` or
`invalid input: unknown field "agent_instance"`. HTTP retains status 400 and code
`invalid`; the HTTP client preserves `errors.Is(err, comitia.ErrInvalid)`. MCP tool
errors expose the same detail in text with `isError: true`, and CLI errors print it
as JSON on stderr. Rejected values and credentials are never included. MCP unknown
argument fields also identify the running client version and suggest a restart.

Call the `server_info` MCP tool with `{}`, send authenticated `POST /v1/server_info`
with `{}`, or run `comitia --server-url URL server-info` with a credential. Any
valid session can discover capabilities, including a session without context
grants. The result contains `version`, `protocol_version` (the MCP revision),
`features`, and `build: {version, revision, dirty}`; HTTP wraps it in `server_info`
and MCP in `result`. The same build identity appears in MCP initialize/discover,
unknown-field guidance and internal failure diagnostics. `dirty` is the string
`true`, `false`, or `unknown`; missing revision metadata is `unknown`. Local source
builds may still use release version `dev`. Source installers stamp `Revision`
and `Dirty`, including when Go VCS stamping is disabled; other builds fall back
to Go's `vcs.revision` and `vcs.modified` settings. Release builders can set
`-ldflags '-X github.com/krimvp/comitia.Version=v1.2.3'`, otherwise the Go module
build version is used when available.

## Compatibility

The feature inventory is maintained in [server_info.go](../server_info.go).
Use optional functionality only when both the daemon's advertised feature and
such fields/tools in your running MCP schema support it. A new daemon does not
upgrade an already running stdio adapter. HTTP delegation additionally requires
`--allow-delegation`; presence and instances remain self-reported.

Choose one instance mode for the whole process:

- **Launcher default:** when `default_agent_instance` is supported and the
  launcher sets `COMITIA_AGENT_INSTANCE`, omit per-call instance arguments and
  use natural stable keys such as `started`.
- **Explicit:** otherwise, when `agent_instances` is supported, use an instance
  object on send and an ID string on cursor/ack. Prefix keys with the instance
  ID for older-server compatibility.
- **Legacy:** if discovery or the optional field is unavailable, use basic
  session-scoped tools and instance-prefixed keys. The cursor is shared by that
  credential's processes. Restart the harness to load upgraded tool schemas.

An unknown optional field error identifies the running client version. Retry
without that field only when the simpler operation still meets the required
contract. Never drop a task fence, mutation receipt or strict recipient check
merely to make a write/read succeed. Changing instance scope on a send retry
creates a different logical write. Full override and opt-out rules remain in
[instance cursors](#independent-acknowledgement-cursors).

[REST wire compatibility](local-daemon.md#rest-wire-compatibility) owns response
spelling, duration units and the two client/server upgrade orders.
[Backup rollback floor](backup-restore.md#supported-rollback-floor) covers restoring
storage into older binaries.


### Operational failures and write readiness

HTTP errors retain their sentinel `code` and include `message`, a generated
`request_id`, and, for conflict/stale/internal failures, a safe recovery `hint`.
An explicit HTTP status override does not change the sentinel code: unknown routes
return status 404 and `not_found`. The HTTP client preserves `errors.Is` and exposes
metadata through `errors.As(err, &operational)` where `operational` is a
`*comitia.OperationalError` and `operational.Info` is the public metadata.
MCP tool errors set `isError: true`, include the hint in text, and expose
`structuredContent: {error: {code, message, hint, request_id}}`.

Dedupe conflicts suggest reusing the original content or choosing a new key;
claim conflicts suggest refreshing the task. Stale claims explain that the latest
`Token` and `Task.LeaseVersion` must be used together. Replay failures suggest a
smaller page or byte budget. Generic internal failures give an operator a request
ID without exposing raw errors, paths, bodies, credentials or private identifiers.
Authorization/not-found errors retain their existing classification without finer
resource-state hints. Deadline/cancellation failures use `deadline_exceeded` or
`cancelled` (HTTP 503); an interrupted write may have committed, so read its state
or reuse its dedupe key (task create/claim/renew: its `operation_id`) before retrying.

Internal failures log only fixed operation labels, error class, generated request
ID, duration and build identity. Caller-supplied request IDs and JSON-RPC IDs are
not diagnostic labels. HTTP returns `X-Request-ID`; MCP forwards a daemon's safe
request ID through remote errors so the operator can correlate both adapters.
SQLite diagnostics distinguish safe primary classes `storage_busy`,
`storage_read_only`, `storage_io` and `storage_full` without printing SQL or error
text; the public error code remains `internal`. Embedded integrations may supply
a `DiagnosticHook` instead of the default log.

Capability discovery and the inspector loading successfully do not establish
write availability. An administrator can call the `storage_status` MCP tool with
`{}`, send `POST /v1/storage_status` with `{}`, or use `StorageStatusAPI` in Go.
The probe shares a 250 ms context budget across authentication and a no-op write
transaction, verifying writer admission/commit. Custom stores must honor context
cancellation. The result includes `write_ready`, `probe_duration_ms`,
`write_attempts`, `write_failures`, `last_write_duration_ms`,
`last_write_failure_duration_ms`, `max_write_duration_ms` and optional
`last_write_error_class`. These process-local counters reset on restart and include
probe transactions; domain rejections such as conflict do not count as storage
failures. A subsequent successful update clears the last error class but retains
the failure count/duration. The probe cannot guarantee free disk space or the
success of a future write. It returns status 200 with `write_ready: false` on
operational write failure; it performs no write probe for unauthorized callers.
