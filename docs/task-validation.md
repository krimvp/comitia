# Task discovery and concurrency validation

Messages can carry a validated, versioned envelope alongside text.
Use `participants --context-id ID` to discover agent session IDs, then address
those IDs rather than display names. See [Message protocol and identity](message-protocol.md)
for payload types, CLI/MCP examples, evidence-backed evaluation reports,
extension points and shared-context visibility.

Use `task-get --task-id ID` to inspect a task, or `task-list --context-id ID`
to list tasks. Optional `--status pending|leased|completed|failed|cancelled` filters the list;
`--limit` defaults to 100 and accepts 1–1000. An empty CLI result is `[]`.
The equivalent MCP tools are `get_task` and `list_tasks`, with the same
context authorization and bound session credential as other tools.
Use `task-list --page` (MCP `page:true`) for `{tasks,next_after,has_more}`.
Continue with `--after-id NEXT_AFTER` (MCP `after_id`); a nonempty cursor implies
page output. Tasks sort by the stored creation timestamp and ID; their opaque
cursor encodes both values and survives deletion of the last returned task.
Keep the context and status filter unchanged while paging. Library callers use
`ListTaskPage(ctx,api,...)`, which detects the optional `TaskPageAPI`.
HTTP `/v1/tasks/list` accepts `page` and `after_id` and returns
the page under `task_page`. Legacy calls still return arrays. Pages reflect
current state and stop near 1 MiB (always one task).
Custom stores may implement optional `TaskPageStore` with
`TasksAfter(contextID,status,createdAt,taskID,limit)`, an exclusive tuple bound
and deterministic order. Existing API/store implementations still compile and
ordinary list calls are unchanged. Without paging capabilities, the helper and
service fall back to the existing list when a 1,000-entry read returns fewer
than 1,000 tasks. At that ceiling, paging returns an explicit unsupported error
because the legacy list cannot establish whether a tail remains.

Task get/list/page expose `lease_expired:true` for leased tasks whose deadline
has passed or whose holder lost claim authority. False is omitted. Stored status
and events stay unchanged. `task-list --claimable` (MCP/HTTP `claimable:true`)
returns pending tasks and expired leases. `--owner-session ID` / `--owner-instance ID`
(MCP/HTTP `owner_session` / `owner_instance`) return matching live leases.
`--mine` (`mine:true`) selects the authenticated caller's live leases, narrowed
to the configured `COMITIA_AGENT_INSTANCE` when available; explicit
`owner_instance` overrides that default. Without an instance default, mine covers
the caller's entire session. Mine excludes `owner_session`; other filters
intersect with each other and stored `status`. Keep every filter unchanged while
paging. Unknown owners in readable contexts return empty lists.
Go uses `TaskFilters`, optional `TaskFilterAPI`, and `ListTasksFiltered` /
`ListTaskPageFiltered`. Legacy APIs reject requested filters; old HTTP servers
produce `httpapi.ErrTaskFiltersUnsupported`. Clients never drop filters on retry.

`task-claim --agent-instance ID` records which process of a shared session holds
a claim (shown as `lease_owner_instance`). Omitted instances default to
`COMITIA_AGENT_INSTANCE` / `COMITIA_AGENT_INSTANCE_LABEL` on CLI claims, MCP
`claim_task`, and the Go HTTP client. Explicit instances replace both defaults;
an explicit empty ID is invalid. `--open-ended` holds the lease until
`task-complete` or `task-release` (capped at the session's expiry), and a
context admin can `task-release` a silent worker's task without its token. Typed
`comitia.approval.v1` messages carry review verdicts on exact commits for merge
gates; see [Approvals and merge gates](message-protocol.md#approvals-and-merge-gates).

## Authoritative events and terminal outcomes

Each successful create, claim, renew, complete, release, failure or cancellation
appends a server-authored `comitia.task-event.v1` message in the task's context,
in the same transaction as the task and any mutation receipt. Replay and wait
return these events to readers with context read access; default reads skip
`renewed` events while page `scanned_through` advances past them. Other
transitions remain visible. Task changes still consume message sequence numbers.
Select all events, including renewals, with
`--schema comitia.task-event.v1` and optionally `--task-id ID`. Messages identify
the authorized actor by `SessionID`, with the task ID in the envelope and kind,
previous/current status, lease version, holder metadata and terminal reason in
the payload. Claim tokens and hashes are absent. Clients cannot append this
reserved schema. Notifications follow commit; reconciliation polling still
handles other server processes and lease expiry. Expiry alone does not mutate
stored status or create an event; reclaim records a new `claimed` event.
Operation receipt recovery and token rotation do not create duplicate events.
Existing databases retain their tasks; historical transitions are not invented.

`failed` and `cancelled` are terminal alongside `completed`. They cannot be
claimed, renewed, released or overwritten. `task-release` continues to return
unfinished work to retryable `pending`.

```sh
comitia task-fail --task-id "$task_id" --claim-token "$claim_token" \
  --lease-version "$lease_version" --reason 'Acceptance checks failed' \
  --operation-id fail-1
comitia task-cancel --task-id "$task_id" --claim-token "$claim_token" \
  --lease-version "$lease_version" --reason 'Work no longer needed'
```

Failure requires the live holder's current token and version. Cancellation
accepts that same fence or context-admin permission without a token. Admins
must supply the current version too, and may cancel pending tasks (initial
version 0) or expired leases. Reasons must be nonempty UTF-8, at most 4096 bytes;
do not put secrets in them. The task exposes `outcome_reason` on get/list/page
and the terminal event carries `reason`. Failure/cancellation accept an optional
session-scoped `operation_id` for exact lost-response recovery. Changed retries
conflict; revoked credentials or lost authority cannot recover receipts.

MCP tools `fail_task` and `cancel_task`, and HTTP endpoints `/v1/tasks/fail` and
`/v1/tasks/cancel`, take `task_id`, `lease_version`, `reason`, optional
`operation_id`, and `claim_token` (required for failure and holder cancellation).
They return a task; HTTP wraps it under `task`. Go callers use the optional
`TaskOutcomeAPI`, or `FailTask`/`CancelTask` helpers, with `TaskOutcomeInput`.
MCP schemas allow `null` for optional properties as an omitted/default value,
including `operation_id` and admin cancellation's `claim_token`. Required
failure claim tokens, lease versions and reasons cannot be null.
Existing exported interfaces keep their method sets. Older APIs/servers return
a clear unsupported error; clients never retry by dropping the requested
outcome or recovery semantics. Old clients can still use ordinary task calls
and read terminal statuses and authoritative events from upgraded servers.

Agent-authored task-status and execution-event messages remain advisory,
including reports of failed/cancelled work. They cannot set the task's outcome.
A task-status child link must name a direct child; if that child serves a task,
its `TaskID` must match the envelope's task ID. Taskless team children may be
shared across tasks.

## Complete lease loop

Run this in the [isolated demo](USING-COMITIA.md#isolated-demo) shell after
setup, or substitute your configured binary/wrapper and context ID. It uses
`jq` and the scoped session's read/write/claim permissions:

```sh
task_json=$("$demo/comitia" task-create --context-id "$context_id" \
  --title 'First task' --body 'Verify the connection and lease loop')
task_id=$(printf '%s' "$task_json" | jq -r '.ID')
claim_json=$("$demo/comitia" task-claim --task-id "$task_id" --lease 5m \
  --agent-instance demo-worker)
claim_token=$(printf '%s' "$claim_json" | jq -r '.Token')
lease_version=$(printf '%s' "$claim_json" | jq -r '.Task.LeaseVersion')

# Renew before expiry, then replace BOTH fields from the same response.
renew_json=$("$demo/comitia" task-renew --task-id "$task_id" \
  --claim-token "$claim_token" --lease-version "$lease_version" --lease 5m)
claim_token=$(printf '%s' "$renew_json" | jq -r '.Token')
lease_version=$(printf '%s' "$renew_json" | jq -r '.Task.LeaseVersion')
completed_json=$("$demo/comitia" task-complete --task-id "$task_id" \
  --claim-token "$claim_token" --lease-version "$lease_version")
printf '%s\n' "$completed_json" | jq '{ID, Status, LeaseVersion}'
```

MCP equivalents (replace placeholders with returned values, and pass versions
as JSON integers). `release_task` is an alternative to completion when
returning unfinished work to pending; do not release a completed task:

| Tool | Arguments | Returned value |
| --- | --- | --- |
| `create_task` | `{"context_id":"CONTEXT_ID","title":"First task","body":"Verify the lease loop"}` | Task object with `ID` and `Status: "pending"` |
| `claim_task` | `{"task_id":"TASK_ID","lease_seconds":300,"agent_instance":{"id":"demo-worker"}}` | `{"Task":{"ID":"TASK_ID","LeaseVersion":1,"Status":"leased",...},"Token":"CLAIM_TOKEN"}` |
| `recover_claim` | `{"task_id":"TASK_ID","lease_version":1,"agent_instance":{"id":"demo-worker"}}` | Same claim shape, with a **new** `Token` and unchanged version/expiry |
| `renew_task` | `{"task_id":"TASK_ID","claim_token":"CLAIM_TOKEN","lease_version":1,"lease_seconds":300}` | Same claim shape, with a **new** `Token` and incremented `Task.LeaseVersion` |
| `complete_task` | `{"task_id":"TASK_ID","claim_token":"NEW_TOKEN","lease_version":2}` | Task object with `Status: "completed"`; no claim wrapper |
| `release_task` | `{"task_id":"TASK_ID","claim_token":"CURRENT_TOKEN","lease_version":2}` | Task object with `Status: "pending"` and incremented version |

The table shows tool values, not JSON-RPC framing: MCP exposes them under
`structuredContent.result`; `content[].text` is a short summary unless the
call passes `compact:false`, which restores the full JSON text. CLI returns the
value directly. REST `tasks/create` and `tasks/complete` wrap it in `task`;
`tasks/claim`, `tasks/recover` and `tasks/renew` wrap the claim in `claim`.

CLI claim/renew defaults to `--lease 5m`. MCP claim/renew defaults to 300 seconds
when `lease_seconds` is omitted or zero, else takes integer seconds. Every adapter
requires leases of 1–86400 seconds. CLI durations use Go syntax (for example `5m`), and
REST/library durations use nanoseconds; do not pass MCP seconds as REST duration.
See the running schema for optional argument support. Every lease is capped at
the holder session's expiry. `open_ended: true` (CLI `--open-ended`) excludes
`lease_seconds` and holds until complete/release or session expiry. Losing claim
permission or revoking the session also makes the lease recoverable.

If you lose a token, read `get_task`/`task-get` for `LeaseVersion`, then use `recover_claim`
(above) or `task-recover --task-id ID --lease-version VERSION --agent-instance INSTANCE`.
Recovery requires the authenticated holder's read and claim access and a live
lease at its current positive version; instance claims require the matching
self-reported ID. CLI/MCP instance defaults apply. Other sessions, including
admins, cannot recover. Expired, terminal, reassigned or stale-version claims
return `stale_claim`; missing/unreadable IDs
return `forbidden`.

Recovery returns `Task` + fresh `Token`, storing only its hash: version, expiry,
timestamps and owner stay unchanged; no event is emitted. Replace tokens
privately; serialize recoveries because each invalidates previous tokens.
Retry lost recovery responses at the current version. Recovery does not renew;
renew before expiry. Raw tokens are never persisted or logged by the server.
HTTP `/v1/tasks/recover` accepts `task_id`, `lease_version`, optional `claim_instance`;
Go uses optional `TaskClaimRecoveryAPI.RecoverClaim`.
Check `server_info` for `task_claim_recovery` and the running MCP schema for
`recover_claim`. Old clients retain ordinary calls; old servers produce an
unsupported error without fallback or dropped constraints.

After renewal, replace `Token` and `Task.LeaseVersion` together; previous/mixed
pairs are stale. After expiry, stop owner-only effects and inspect ownership
before reclaiming. Stale claims do not authorize blind completion retries.

Create/claim/renew accept session-scoped `operation_id` (CLI `--operation-id`,
at most 256 bytes). Persist a distinct key and original arguments before each
mutation; retry identically after lost responses. SQLite commits non-secret
receipts atomically and retains them across restart. Identical create retries
return the original snapshot. Claim/renew retries rotate only the token hash
while the recorded lease remains live, caller-owned and current; version,
expiry and outcome stay unchanged. Replace tokens and serialize retries before
new operations. Retry renewal with its original input token/version/key,
not the recovered token. Changed arguments or reuse for another mutation
conflict. Completion, release, expiry, reassignment or another renewal makes
the old operation conflict without a token; inspect the task and use a new key.

Keys span methods/contexts within the session; instance IDs belong to claim
arguments, not receipt scope. Retrieval rechecks context permissions. Receipts
do not make external jobs idempotent; give them stable identities and enforce
ownership separately.
HTTP create nests `operation_id` inside `task`; claim/renew use the top level.
Go uses `TaskInput.OperationID`, `ClaimTaskWithOperation`, `RenewClaimWithOperation`.
Servers without `task_mutation_receipts` reject `operation_id` before mutation;
HTTP clients return `httpapi.ErrTaskMutationReceiptsUnsupported` without dropping
it. Upgrade before relying on receipts. Without a key, use holder recovery;
after expiry, use authorized release/reclaim. `get_task` never reveals tokens.
Release with the current token/version; admins may omit the token, not version.

## Validation

Build, then run the process-level stress harness:

```sh
go build -o bin/comitia ./cmd/comitia
python3 scripts/stress_agents.py --binary "$PWD/bin/comitia" --workers 12 --messages 100
```

`--messages` is the number of logical writes **per worker**. The harness uses a
temporary database, checks retry identity, paginated replay, exclusive claims,
expired-lease recovery and stale-completion rejection, and emits a JSON summary.
A failure names the failing CLI command (and dedupe key); CI runs it three
times. Concurrent CLI processes wait up to 15 s for the SQLite write lock, and
opening an up-to-date database does not take the write lock.
Workers are subprocesses, not language-model agents.
See the [six-agent engineering trial](history/release-readiness-20260909.md) for
the separate real-agent collaboration results and their limitations.

To compare single-agent, multi-agent, and Comitia-coordinated runs using a
balanced trial matrix, pinned model pricing, and fixed quality checks, see the
[cost and quality evaluation harness](evaluation.md). The harness calls an
external runner because Comitia coordinates agents but does not execute them.
Runner integrations can also publish validated, task-linked
[execution telemetry](execution-telemetry.md) through the ordinary message
API. This records bounded lifecycle and usage measurements without storing
prompts, responses, tool output, or private reasoning.

The onboarding regression checks execute the demo, MCP lease examples and the
macOS shared-context wrapper recipe with a temporary HOME and database (no
installed service changes). From the repository root, with Python 3, `jq` and
the Go 1.24.13 toolchain available, and port 18080 free:

```sh
python3 -m unittest scripts/test_docs_onboarding.py
```

