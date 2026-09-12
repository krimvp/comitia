# Extended engineering and recovery evaluation

This extends, rather than replaces, `evaluation/live`. It uses the existing `scripts/evaluate_agents.py` runner interface, validation, scoring, aggregation, and cost accounting. The experiment pins the same repository and model as the first evaluation. All model prompts, grading code, storage adapters and specification are hashed before execution. No changes to production Comitia code are necessary.

## Workload and modes

A five-stage workflow builds three interoperating Python modules and a 120–220 word engineering handoff: architecture, ledger implementation, outbox implementation, integration, and final review. Ledger/outbox engineers run concurrently in both multi-agent modes. The reviewer receives four public smoke-test results; eight other behavioral/rubric checks remain hidden. All 12 check groups have equal weight; passing requires all 12. A reference implementation and deliberately broken variants validate the grader. Surrounding prose is tolerated by a predeclared JSON extraction rule.

- **single-agent**: one logical engineer, five sequential fresh model calls, replaying all previous stage outputs as context. This is explicit context reconstruction, not a native CLI conversation resumed by session ID. The same durable file store checkpoints results.
- **multi-agent**: five distinct role prompts, separate fresh model calls, dependency-scoped context, two concurrent implementation calls. A custom local file-backed coordination baseline uses a lock, atomic file replacement, deduplicated messages, expiring claims, and generation fencing. It is deliberately a capable baseline, not memory-only messaging.
- **comitia**: identical roles, dependencies, prompts and concurrency to multi-agent. Real Comitia CLI operations implement durable messages, scoped credentials, claims, replay and completion. The adapter only exposes published results associated with the currently completed lease generation. Comitia's message API by itself does not prevent a stale worker from sending a message.

The baseline's publish/complete is one locked file update; Comitia uses separate message and task operations, filtered on read. We do not claim these adapters have identical guarantees at every untested instruction boundary. Both store credentials/control information outside model context. The model never chooses Comitia commands; the adapter schedules a fixed graph. Models generate full files in JSON, which are executed by an independent test subprocess; they have no shell/editor/network tools. The baseline is local and bespoke, not a benchmark of another coordination product.

## Fault profiles

The identical engineering specification is run under three failure conditions, with two repetitions per mode: 18 trials, normally five model calls each, six for lease takeover. These are three fault conditions on one larger workload, not three independent engineering domains. The previous six-domain evaluation remains the breadth evidence.

1. Clean: no injected interruption.
2. Handoff crash: after architecture result publication and task completion but before consumer acknowledgement, the engine stops at a barrier. Its parent sends actual SIGKILL and launches a new Python process. Recovery must find the existing result and proceed without another architecture model call. The lost-ack publication is retried; the authoritative result must remain unchanged.
3. Lease takeover: the ledger claim expires while its model computes a draft. After both parallel engineers have returned (the outbox result is durably saved), the engine is SIGKILLed before the ledger draft is published. A new process recovers saved work and a replacement generates a new ledger draft under a new lease. The delayed old result is then submitted using its expired claim. Recovery must reject it and retain the replacement. Original work is included in usage/cost, even though deliberately discarded.

These barriers isolate fully billed lost computation and durable handoff behavior. No provider model request is in flight at the deliberate SIGKILL points. This is not a mid-token cancellation, host reboot, disk-loss, distributed-partition, or arbitrary-instruction crash test. Natural model-client timeouts are still recorded as failures with missing usage flagged. At each deliberate restart the scheduler obtains outputs from its selected store; it does not recover model context from audit files.

## Measurements and limits

Report final behavioral correctness separately from recovery invariants. Preserve integration and final scores, raw responses, prompts, usage, timed storage operations, engine PIDs, injected SIGKILL events, replayed stage names, stale completion results and final task generations. Communication checks verify length and topic coverage; manual review is needed for correctness and unsupported claims. Two repetitions are exploratory, insufficient for broad significance claims.

Model: `claude-haiku-4-5-20251001`; same CLI options as the earlier eval, no tools/hooks/skills/MCP or prompt caching. Default model reasoning effort is unchanged. Each model-client call is limited to 600 seconds, each trial to 4000 seconds. The CLI has no sampling seed control in this setup. List-price equivalents use the earlier pinned September 11, 2026 Anthropic pricing snapshot: input $1/M, output $5/M, cached input $0.10/M. These are not subscription charges. Every returned model usage record counts, including deliberate discarded drafts and failed returned results. Missing timeout usage is incomplete, not free. Local computing cost is excluded consistently; storage and restart time count in elapsed time.

Trials run sequentially; modes rotate by condition and repetition. Only the two independent implementation roles overlap inside multi-agent trials. The same engineer's sequential run is not an equal-context/token-budget arm, so its comparison includes workflow/context/concurrency differences. The two multi-agent modes isolate coordination more closely.

## Reproduction

From the repository root:

```sh
python3 -m unittest discover -s evaluation/recovery -p 'test_*.py'
python3 evaluation/recovery-long/run_matrix.py
python3 scripts/evaluate_agents.py summarize --spec evaluation/recovery-long/benchmark.json --observations evaluation/recovery-long/observations/*.json --output evaluation/recovery-long/revalidated.json --markdown evaluation/recovery-long/revalidated.md
```

The recovery tests use reference responses only, never paid models; they exercise nine full process lifecycles and concurrent claims against both real storage implementations. Those synthetic test runs are separate from performance results. The live matrix refuses to reuse changed execution files or silently overwrite incomplete trial artifacts. Inspect interrupted cells explicitly before choosing whether to conduct a separately labeled rerun.

## Extended time budget

The user requested a longer time budget after the initial series began. The original 225-second series is preserved in `evaluation/recovery` as calibration evidence; this separate full matrix uses 600 seconds per call and 4000 seconds per trial. Model settings, prompts, grading rules and coordination logic are identical. Do not pool the two time-budget series for primary results.

## Unplanned host interruption

The first long-budget clean single-agent trial was affected by closed-lid host sleep and timed out; usage is incomplete. macOS monotonic elapsed timers exclude host sleep, so this trial also has a much larger civil-time span. Preserve it as an environment-affected observation. A separate three-mode clean control is predeclared in `awake-control-spec.json`; repeat the entire mode triple rather than selecting only the failed mode for rerun. Do not silently replace or pool observations across specifications. The main 18-trial matrix continues unchanged.

On macOS, keep the lid open during live inference. To prevent ordinary idle sleep
for the process lifetime, launch with `caffeinate -i python3
evaluation/recovery-long/run_matrix.py`. Closed-lid sleep still overrides this.
`awake_guard.py MATRIX_PID` is an optional external controller that pauses the
outer scheduler after a civil/monotonic clock gap and resumes after 90 awake
seconds. It does not silently retry failed cells or fix an already expired claim.

`analyze.py` retains all as-run results and separately excludes entire matched
condition/repetition triples affected by host sleep. Civil completion timestamps
are captured once in `timing-wall-evidence.json`, so later copying the files does
not change that audit. Monotonic timing on this macOS/Python combination excludes
host sleep; use the environment flags before comparing elapsed times. The
unexpected pauses also expose an adapter limitation: expired claims fail the
workflow unless the explicitly modeled restart/reclaim path is used.


## Completed-series analysis additions

`analyze.py --control`, `audit_components.py --control`, `audit_handoffs.py --control`, and `audit_rollback.py --control` produce separately prefixed clean-control diagnostics. Omitting the flag analyzes the main series. `status.py --control` reports the control run.

The rollback diagnostic also substitutes enqueue functions that raise ValueError or RuntimeError before or after normal mutation. These post-hoc injections probe broader failure handling than the frozen grader's ordinary conflicting-key case. They are applied uniformly to completed final solutions, validated against the reference implementation, and do not change as-run scores or feed evaluated model sessions. Original collision probes cover reserve, capture and refund.

`make_report.py` requires all 18 main observations and all three clean controls plus their analyses, preventing an incomplete result from being presented as the completed experiment.
