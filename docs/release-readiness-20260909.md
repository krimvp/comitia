# Six-agent release-hardening trial — 2026-09-09

## Outcome

Six actual GPT-5.6 Luna agents, all at low reasoning effort, were launched concurrently to work on the Comitia codebase. They produced task-discovery adapters, tests, a process stress harness and a peer review. All substantive assignments, questions, findings and handoffs used the real Comitia CLI against shared SQLite, with a separate scoped session for each agent.

This was a supervised integration exercise, not autonomous completion by the cheapest agents. Their first passes were incomplete. The orchestrator issued six follow-up tasks through Comitia, then strengthened tests and integrated the changes. Agent task completion did not count as acceptance.

The integrated code at `2c5e6fe8be0b8c7817b137503c2985988d351ab6` passed [CI](https://github.com/krimvp/comitia/actions/runs/34294802722): static analysis, the full race-enabled Go test suite, ten repeated root-package security/crash test runs, binary build and process stress. Subsequent source formatting used that run's actual gofmt diff.

## Real work and ownership

| Agent | Work |
| --- | --- |
| cli | Task get/list CLI commands and tests |
| mcp | Task get/list MCP tools and tests |
| security | Cross-context ownership and revocation tests; adversarial peer review |
| recovery | Subprocess termination and durable-state recovery test |
| workload | Python subprocess stress harness |
| auditor | Independent inspection and release-gate findings |

The repo artifacts were shared through the checkout, but coordination used Comitia. Native agent lifecycle tools provided bootstrap instructions and completion notices only. Agents did not read the coordination database directly. Comitia did not launch or execute the models.

The [transcript](validation/luna-six-agent-20260909.json) contains 54 persisted messages: 37 from agents, 17 from the orchestrator, including 22 peer-to-peer messages across 16 directed peer relationships. There were six original tasks and six follow-up tasks, all persisted as completed. Session-specific acknowledgement cursors were checked and all six worker sessions were revoked afterward.

## Shipped changes

- CLI `task-get` and `task-list`, with required identifiers, status filters, bounded limits and an empty-list JSON array.
- MCP `get_task` and `list_tasks`, strict argument decoding and credential binding. Omitted limit defaults to 100; explicit zero is rejected.
- Central `ListTasks` rejection of invalid statuses, so library and HTTP callers also receive validation.
- Tests for cross-context task reads/claims, stolen claim-token rejection, concurrent revoke/read ordering, and post-revocation denial.
- A killed-child recovery test that waits for committed message/cursor/lease state, kills the exact child while it is blocked, reopens SQLite, verifies the recovered lease, reclaims after deterministic expiry and rejects the actual old claim.
- A repeatable process stress harness and CI steps, including a formatting gate and retained stress summary.

## Verified workload

The new CI-built binary was downloaded and its artifact SHA-256 verified before the larger local run.

| Check | Observed |
| --- | --- |
| Actual model agents | 6, Luna low |
| Local subprocess stress workers | 24 |
| Logical writes | 6,000 |
| Checked duplicate retries | 600 |
| Full replay | 6,000 contiguous sequences, exact logical keys/bodies, unique IDs |
| Contended task | 24 claim attempts, exactly one winner; other results must be conflicts |
| Expiry recovery | Higher lease version acquired, old completion rejected, fresh completion accepted |
| Acknowledgement | Full cursor preserved after another CLI process reopened the DB |
| Negative control | Deliberately corrupted replay body made the harness exit nonzero |

The large local run reported 7.323 seconds in this environment. That is an observation, not a portable throughput claim. CI separately ran 12 workers × 100 logical writes. Stress workers share a test owner credential; distinct scoped identities were exercised by the real agents and authorization tests.

[Machine-readable evidence](validation/luna-six-agent-20260909-checks.json) records the final task/cursor checks and larger workload result. No bearer credentials or lease tokens are stored in these evidence files.

Reproduce the process test:

```sh
go build -o bin/comitia ./cmd/comitia
python3 scripts/stress_agents.py --binary "$PWD/bin/comitia" --workers 24 --messages 250
go test -race ./...
go test -race -count=10 -run 'TestCrashRecovery|TestSecurity' .
```

The model-agent run requires an external agent harness; the Python stress script does not recreate model conversations.

## What the cheapest-agent experiment revealed

1. CLI and MCP initially omitted assigned tests. Follow-up tasks produced them.
2. Security caught a real defect in the newly authored recovery test: its child used wall-clock time while the parent fixture used January 2026, invalidating credentials. Recovery corrected the clock.
3. The initial stress harness reported deduplication and contention success without sufficient assertions and replayed only the first 1,000 messages. Follow-up work improved it, but the orchestrator still had to enforce claim failures, full content validation, and expired-lease checks.
4. The recovery follow-up still risked a clean child exit through stdin EOF and used a fabricated stale token. Integration supplied a held-open pipe and the real old claim.
5. The audit draft became stale as peers changed files. The final gate depended on source inspection and CI, not peer endorsement alone.

Luna was selected using the [official model catalog](https://learn.chatgpt.com/docs/models) as the low-cost available worker option. No per-agent billing/token ledger was exposed, so no dollar cost or cost-savings figure is claimed. The orchestrator remained a higher-capability model; this was not an all-Luna system.

## Remaining limitations

No production-readiness claim. This bounded run does not establish long-duration stability, remote/network fault recovery, power-loss durability, untrusted filesystem isolation, or autonomous correctness on a large project. The crash test kills a process after committed operations, not during an uncommitted transaction or a machine power failure.

Embedded mode still trusts the host. Recipients and message types remain JSON-body conventions; messages are visible to all readers of the authorized context. Polling is still required. The higher-level application must separate “agent says done” from independently accepted work.

The tested coordination paths behaved correctly. The main failure mode observed in this experiment was incomplete agent verification, not lost Comitia messages or duplicate task ownership.
