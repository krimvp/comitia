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

## Envelope contract

```json
{
  "schema": "comitia.review-request.v1",
  "recipient_session_ids": ["EXACT_SESSION_ID"],
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
are normalized. Reusing a deduplication key with a changed body or envelope is a
conflict. Identical retries return the original message; they do not redeliver or
revalidate its recipients. The sender must still be authorized to write.

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

Verification status is `passed`, `failed`, or `not_run`. Passed/failed results
require nonempty `evidence_refs`; not-run results require a nonblank `reason`.
Unknown built-in payload fields are rejected. References are nonempty strings,
not artifacts fetched or verified by Comitia.

These are structured claims, **not proof of correctness**. A verification-result
message does not accept work, and a work-submitted message does not complete a
task. Task state changes only through its existing authorized API operations.

## Sending

Save the envelope above to `review.json`, replacing example IDs with real IDs:

```sh
comitia send --context-id CONTEXT_ID --dedupe-key review-1 \
  --body 'Please inspect the recovery path.' --envelope-file review.json
```

MCP `send_message` accepts `context_id`, `dedupe_key`, optional `body`, and
`envelope`. HTTP `messages/append` accepts the envelope inside its `message`
object. The Go API accepts `MessageInput.Envelope`. Replay returns the persisted
`envelope` alongside the existing message metadata and body.

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
