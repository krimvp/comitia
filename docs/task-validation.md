# Task discovery and concurrency validation

Messages can carry a validated, versioned envelope in addition to free text.
Use `participants --context-id ID` to discover agent session IDs, then address
those IDs rather than display names. See [Message protocol and identity](message-protocol.md)
for payload types, CLI/MCP examples, evidence-backed evaluation reports,
extension points and shared-context visibility.

Use `task-get --task-id ID` to inspect a task, or `task-list --context-id ID`
to list tasks. Optional `--status pending|leased|completed` filters the list;
`--limit` defaults to 100 and accepts 1–1000. An empty CLI result is `[]`.
The equivalent MCP tools are `get_task` and `list_tasks`, with the same
context authorization and bound session credential as other tools.

`task-claim --agent-instance ID` records which process of a shared session holds
a claim (shown as `lease_owner_instance`). Omitted instances default to
`COMITIA_AGENT_INSTANCE` / `COMITIA_AGENT_INSTANCE_LABEL` on CLI claims, MCP
`claim_task`, and the Go HTTP client. Explicit instances replace both defaults;
an explicit empty ID is invalid. `--open-ended` holds the lease until
`task-complete` or `task-release` (capped at the session's expiry), and a
context admin can `task-release` a silent worker's task without its token. Typed
`comitia.approval.v1` messages carry review verdicts on exact commits for merge
gates; see [Approvals and merge gates](message-protocol.md#approvals-and-merge-gates).

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
| `renew_task` | `{"task_id":"TASK_ID","claim_token":"CLAIM_TOKEN","lease_version":1,"lease_seconds":300}` | Same claim shape, with a **new** `Token` and incremented `Task.LeaseVersion` |
| `complete_task` | `{"task_id":"TASK_ID","claim_token":"NEW_TOKEN","lease_version":2}` | Task object with `Status: "completed"`; no claim wrapper |
| `release_task` | `{"task_id":"TASK_ID","claim_token":"CURRENT_TOKEN","lease_version":2}` | Task object with `Status: "pending"` and incremented version |

The table shows tool values, not JSON-RPC framing: MCP exposes them under
`structuredContent.result` and as JSON in `content[].text`. CLI returns the
value directly. REST `tasks/create` and `tasks/complete` wrap it in `task`;
`tasks/claim` and `tasks/renew` wrap the claim in `claim`.

CLI claim/renew defaults to `--lease 5m`. MCP claim/renew defaults to 300 seconds
when `lease_seconds` is omitted or zero; a nonzero duration must be an integer from
1 through 86400 seconds. CLI durations use Go syntax (for example `5m`), and
REST/library durations use nanoseconds; do not pass MCP seconds as REST duration.
See the running schema for optional argument support. Every lease is capped at
the holder session's expiry. `open_ended: true` (CLI `--open-ended`) excludes
`lease_seconds` and holds until complete/release or session expiry. Losing claim
permission or revoking the session also makes the lease recoverable.

After **every** successful renewal, replace `Token` and `Task.LeaseVersion`
together. The previous pair, or either mixed pair, is stale and cannot complete
or renew. If the lease expires, stop owner-only side effects and claim again;
another worker may already own it. A stale-claim error is an ownership failure,
not permission to retry completion blindly. Claim and renewal are not
idempotent: after an uncertain network response, inspect `get_task`; it does
not reveal the new token. Release/reclaim through an authorized coordinator if
the renewed token is lost. To abandon work, release with the current pair; a
context admin may omit the token but must supply the current version.

## Validation

Run the reproducible process-level stress harness after building:

```sh
python3 scripts/stress_agents.py --binary "$PWD/bin/comitia" --workers 12 --messages 100
```

`--messages` is the number of logical writes **per worker**. The harness uses a
temporary database, checks retry identity, paginated replay, exclusive claims,
expired-lease recovery and stale-completion rejection, and emits a JSON summary.
A failure names the CLI command (and dedupe key) that failed; CI runs it three
times. Concurrent CLI processes wait up to 15 s for the SQLite write lock, and
opening an up-to-date database does not take the write lock.
These workers are subprocess clients, not language-model agents.
See the [six-agent engineering trial](release-readiness-20260909.md) for
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
