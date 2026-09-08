# Live Sol-low collaboration trial — 2026-09-08

## Result

Two actual `gpt-5.6-sol` agents at low reasoning effort solved a constrained scheduling problem using Comitia for all substantive coordination. Both Comitia tasks reached `completed`. The agreed optimum, 10 time units, matched an independent exhaustive enumeration by the orchestrator.

Tested source: `c8b6667f290a79372b0cd3537bb73950ed1695f8`.
[CI run](https://github.com/krimvp/comitia/actions/runs/34280526428) passed `go vet ./...`, `go test -race ./...`, and the Linux binary build.
The downloaded artifact's SHA-256 matched GitHub's recorded digest.

## Actual topology

This environment had no Go compiler. CI built the real Comitia binary, which ran locally. Localhost listeners were unreachable across separate tool executions, although a server and client worked inside one execution. The agent trial therefore used the supported embedded SQLite topology.

Each agent received its own read/write/claim credential scoped to one work context. A thin Python command wrapper invoked the real CLI for messages, claims, acknowledgements, and completion. It did not transport messages through files or access SQLite directly. The shared file was Comitia's own SQLite database. HTTP parity checks used the same database.

Native agent spawning supplied only bootstrap instructions, role, and how to access Comitia. The problem and task IDs were retrieved from Comitia. Native agent messages were not used for work coordination. Native final responses only said “Comitia task completed.”

## Message evidence

[Full transcript](sol-low-trial-20260908.json) records 11 persisted messages, including 6 authored by the agents:

| Sequence | Sender → recipient | Meaning |
| --- | --- | --- |
| 1–3 | Owner → agents | Problem and two task assignments |
| 4 | Planner → reviewer | Proposed schedule and optimality proof |
| 5 | Owner → all | Retry probe |
| 6 | Reviewer → planner | Independent result and question about proof |
| 7 | Planner → reviewer | Case-by-case lower-bound explanation |
| 8 | Owner → all | Concurrent retry probe |
| 9 | Reviewer → planner | Approval |
| 10 | Reviewer → owner | Independent verdict |
| 11 | Planner → owner | Agreed final answer |

Recipients and message types are conventions in JSON message bodies, not server-enforced private inboxes. Both agents could read the entire authorized context.

## Problem and answer

Two identical workers execute non-preemptive jobs A=3, B=2, C=4, D=2, E=3, F=1.
Dependencies: A→C; A,B→D; B→E; C,D,E→F.
C and E share an exclusive device. Start times are nonnegative integers.

| Worker | Schedule |
| --- | --- |
| 1 | B 0–2; E 2–5; C 5–9; F 9–10 |
| 2 | A 0–3; D 3–5 |

If E precedes C, B→E implies E finishes no earlier than 5, C no earlier than 9, and F no earlier than 10. If C precedes E, A→C implies C finishes no earlier than 7, E no earlier than 10, and F no earlier than 11. Thus 10 is optimal.

The independent checker enumerated integer starts with dependency, worker-capacity and device constraints. It found no feasible schedule at horizon 9 and found a schedule at 10. Shorter schedules would also fit horizon 9.

## Additional live checks

- Separate-process retries: 12 simultaneous identical writes produced one message ID and sequence.
- Reusing the same deduplication key with changed content returned HTTP 409.
- An agent reading an unrelated context returned HTTP 403 without message payloads.
- HTTP could read the state written through embedded CLI processes.
- Both agent tasks were completed and owned by distinct sessions.
- Persisted acknowledgement cursors were planner=10 and reviewer=7; these reflect consumed peer messages, not necessarily the final context sequence.
- A lease expired, was reclaimed with a newer version, and rejected completion using the old claim.
- Reopening the database preserved messages, tasks and cursors. This was clean process reopen, not power-loss testing.
- Both agent sessions were revoked after completion; subsequent replay attempts were unauthenticated.

[Machine-readable check results](sol-low-trial-20260908-checks.json) contain the observed values. No credentials or claim tokens are included.

## Limits and usability findings

This is a successful small real-agent trial, not evidence of production readiness or prolonged autonomous operation. It does not test hostile agents, crash consistency under abrupt termination, remote network faults, load at scale, or real MCP client compatibility.

Agents polled for messages. Task discovery is available in the HTTP API, but the current CLI and MCP adapter lack task-list operations; task IDs were supplied through messages. Per-recipient routing is application convention. Embedded mode assumes trusted filesystem access and cannot isolate agents that bypass the API.

The trial service is not a persistent hosted deployment. CI now retains a tested Linux artifact for seven days, making subsequent local trials easier.
