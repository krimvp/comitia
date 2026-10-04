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
