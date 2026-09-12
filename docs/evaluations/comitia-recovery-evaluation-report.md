# Comitia: larger engineering tasks and recovery

**Finding:** Comitia successfully preserved completed work across controlled restarts and rejected stale completions. Its code score tied the durable file-based team (97.7%); its overall score was 2.1 percentage points higher because of one handoff. It cost 2.1% more and took 2.4% longer than that team in matched main runs. These small differences do not demonstrate that Comitia improves model reasoning. Compared with the sequential engineer, Comitia was 7.5% faster and 8.4% more expensive, with a 4.2-point higher overall score. The separate clean control reversed the quality ordering (sequential 12/12, files 11/12, Comitia 10/12), further limiting any claim of a stable advantage.

This extension executes one multi-file engineering workload under three fault profiles: clean operation, process death after publishing a handoff, and lease takeover with a delayed stale result. Five roles produce an architecture, two modules, integration, and final review. Two implementers run concurrently in team modes; the sequential engineer uses the same stages with accumulated context.

All modes use `claude-haiku-4-5-20251001` through Claude Code 2.1.263. The larger budget is 600 seconds per model call and 4,000 per trial. The main series contains 18 trials (two repetitions per mode/condition); a separately labeled three-mode clean control follows. Two earlier 225-second calibration trials are preserved and not pooled.

## Comparable main-series results

4 trials were affected by closed-lid sleep. Ordinary-performance comparisons below exclude entire affected condition/repetition triples, leaving 4 matched conditions per mode. All 18 original rows remain in the evidence. These are small-sample pilot results, not broad significance claims.

| Workflow | Checks | Code checks | Full passes | Completed | Captured cost | Input / output tokens | Mean elapsed | Mean coordination |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| One engineer | 87.5% | 95.5% | 0/4 | 4/4 | $1.4001 | 97,107 / 260,601 | 528.0s | 0.025s |
| Five roles + files | 89.6% | 97.7% | 0/4 | 4/4 | $1.4872 | 73,765 / 282,695 | 476.9s | 0.024s |
| Five roles + Comitia | 91.7% | 97.7% | 1/4 | 4/4 | $1.5181 | 73,684 / 288,881 | 488.3s | 0.897s |

Matched runs per mode: clean: 1, handoff-crash: 1, lease-takeover: 2. Metrics weight these runs equally. Costs and tokens are totals for the matched runs; elapsed and coordination times are per-trial means. Checks are 12 equally weighted groups: 11 code-behavior groups and one handoff length/topic group. A full pass requires all 12. Communication correctness is also inspected manually; passing the automated rubric is not proof of flawless writing. Costs are recorded list-price equivalents, not subscription charges. Cost completeness for these matched rows: One engineer 4/4, Five roles + files 4/4, Five roles + Comitia 4/4.

Comitia minus the file-based team: +2.08 percentage points in check score; paired bootstrap 95% interval [+0.00, +6.25]. This interval only resamples the small fixed set of matched runs; it is not a production-wide confidence claim.

## Separate clean control

This repeats all three modes after the initial clean triple encountered host sleep. It is not a selective retry of only a failed mode and is not pooled into the main series.

| Workflow | Checks | Code checks | Status | Captured cost | Input / output tokens | Elapsed | Host sleep |
|---|---:|---:|---|---:|---:|---:|---|
| One engineer | 100.0% | 100.0% | completed | $0.3028 | 18,518 / 56,859 | 442.0s | no |
| Five roles + files | 91.7% | 100.0% | completed | $0.3304 | 16,735 / 62,739 | 439.6s | no |
| Five roles + Comitia | 83.3% | 90.9% | completed | $0.3114 | 16,978 / 58,890 | 404.4s | no |

The clean control reversed the main-series quality ordering: the sequential engineer passed 12/12, the file-based team 11/12, and Comitia 10/12. Comitia finished in 404.4 seconds versus 439.6 and 442.0 seconds, respectively. Its reviewer corrected handoff length but removed explicit database-transaction guidance, and missed an empty-order-ID validation bug. This single control reinforces that the small main-series lead is not a stable quality benefit.

## Recovery and review

- One engineer: 4/4 controlled recoveries verified outside host-sleep interruptions. In matched completed trials, review improved 2, worsened 0, and tied 2 aggregate check scores.
- Five roles + files: 3/3 controlled recoveries verified outside host-sleep interruptions. In matched completed trials, review improved 0, worsened 0, and tied 4 aggregate check scores.
- Five roles + Comitia: 4/4 controlled recoveries verified outside host-sleep interruptions. In matched completed trials, review improved 1, worsened 0, and tied 3 aggregate check scores.

The 6 takeover trials spent 87,895 tokens ($0.3930) on deliberately unpublished drafts. That work is included in costs. Recovery preserved completed architecture/outbox work; a replacement recomputed the unpublished ledger. In verified takeovers, stale completion was rejected without changing the replacement result.

Comitia permits message publication independently of task completion. The adapter therefore accepts only the message associated with the completed lease generation; a rejected stale task completion alone is not a guarantee that no stale message was sent. The file baseline implements its own locks, durable writes, deduplication, leases, and generation checks.

## Individual trials

| Condition | Rep | Workflow | Checks | Status | Host sleep | Seconds | Captured cost | Recovery |
|---|---:|---|---:|---|---|---:|---:|---|
| clean | 0 | Five roles + Comitia | 0.0% | failed | yes | 40.1 | $0.0134 | None |
| clean | 0 | Five roles + files | 0.0% | failed | yes | 50.8 | $0.0146 | None |
| clean | 0 | One engineer | 0.0% | timeout | yes | 796.8 | $0.1391 | None |
| clean | 1 | Five roles + Comitia | 91.7% | completed | no | 490.8 | $0.3612 | None |
| clean | 1 | Five roles + files | 91.7% | completed | no | 406.9 | $0.3715 | None |
| clean | 1 | One engineer | 91.7% | completed | no | 422.9 | $0.3075 | None |
| handoff-crash | 0 | Five roles + Comitia | 91.7% | completed | no | 389.7 | $0.3147 | True |
| handoff-crash | 0 | Five roles + files | 0.0% | failed | yes | 366.1 | $0.1819 | False |
| handoff-crash | 0 | One engineer | 91.7% | completed | no | 525.5 | $0.3464 | True |
| handoff-crash | 1 | Five roles + Comitia | 100.0% | completed | no | 448.5 | $0.3472 | True |
| handoff-crash | 1 | Five roles + files | 91.7% | completed | no | 412.0 | $0.3281 | True |
| handoff-crash | 1 | One engineer | 91.7% | completed | no | 417.7 | $0.3033 | True |
| lease-takeover | 0 | Five roles + Comitia | 83.3% | completed | no | 479.9 | $0.4126 | True |
| lease-takeover | 0 | Five roles + files | 83.3% | completed | no | 511.6 | $0.3858 | True |
| lease-takeover | 0 | One engineer | 75.0% | completed | no | 633.1 | $0.3999 | True |
| lease-takeover | 1 | Five roles + Comitia | 91.7% | completed | no | 533.8 | $0.3971 | True |
| lease-takeover | 1 | Five roles + files | 91.7% | completed | no | 577.1 | $0.4018 | True |
| lease-takeover | 1 | One engineer | 91.7% | completed | no | 638.4 | $0.3893 | True |

macOS monotonic elapsed time excludes host sleep. The affected calls had much larger civil-time spans, captured separately. Returned usage is retained; missing timeout usage is incomplete rather than free. Unexpected long pauses exposed a real adapter limitation: expired claims stop the workflow rather than triggering automatic reclaim/recompute. Both stores protected ownership, but neither surrounding workflow self-healed from those unplanned pauses.

## Failure inspection

The code failures included accepting empty order IDs, calculating claim expiry from a later caller’s requested lease instead of the stored lease, and accepting completion after expiry. Review repaired some integration rollback mistakes but frequently left writing constraints unmet. These generated-application defects are separate from Comitia’s coordination-store behavior.

A post-hoc fault-injection diagnostic replaces enqueue with a function that raises ValueError or RuntimeError, before or after adding an item. This extends the primary grader, which only exercises a normal conflicting-key ValueError. It is applied uniformly to available final outputs and never changes original scores or model prompts. Several implementations catch only ValueError or restore only the ledger, so high code scores do not establish robust rollback under broader failures. The reference implementation passes all seven rollback probes.

Main series: 7/14 final solutions fail at least one injected enqueue-error probe. Affected trials: order-system-clean--1--comitia, order-system-clean--1--single-agent, order-system-handoff-crash--0--comitia, order-system-handoff-crash--1--multi-agent, order-system-lease-takeover--0--multi-agent, order-system-lease-takeover--0--single-agent, order-system-lease-takeover--1--comitia.
Separate clean control: 2/3 final solutions fail at least one injected enqueue-error probe. Affected trials: order-system-clean--0--comitia, order-system-clean--0--multi-agent.

- order-system-clean / rep 1 / Five roles + Comitia: handoff: AssertionError: word count 324
- order-system-clean / rep 1 / Five roles + files: handoff: AssertionError: word count 351
- order-system-clean / rep 1 / One engineer: handoff: AssertionError: word count 251
- order-system-handoff-crash / rep 0 / Five roles + Comitia: handoff: AssertionError: word count 344
- order-system-handoff-crash / rep 0 / One engineer: handoff: AssertionError: word count 373
- order-system-handoff-crash / rep 1 / Five roles + files: handoff: AssertionError: word count 249
- order-system-handoff-crash / rep 1 / One engineer: handoff: AssertionError: word count 223
- order-system-lease-takeover / rep 0 / Five roles + Comitia: handoff: AssertionError: word count 325; ledger_validation: AssertionError: expected ValueError
- order-system-lease-takeover / rep 0 / Five roles + files: handoff: AssertionError: word count 231; ledger_validation: AssertionError: expected ValueError
- order-system-lease-takeover / rep 0 / One engineer: handoff: AssertionError: word count 247; outbox_dedupe_snapshot: AssertionError: ; outbox_fencing: AssertionError:
- order-system-lease-takeover / rep 1 / Five roles + Comitia: handoff: AssertionError: word count 247
- order-system-lease-takeover / rep 1 / Five roles + files: handoff: AssertionError: word count 407
- order-system-lease-takeover / rep 1 / One engineer: handoff: AssertionError: word count 315

[Manual audit with root causes and documentation examples](../../evaluation/recovery-long/manual-audit.md). Reviewer fixes and newly introduced failures, per-role usage, retained/lost work, and actual implementation overlap are in [the detailed analysis](../../evaluation/recovery-long/analysis.json).

## Interpretation

Comitia helps when a workflow needs saved handoffs, exclusive ownership and safe worker replacement: those mechanisms worked in the controlled recovery trials. The comparison does not show an advantage over a baseline that already implements those mechanisms itself. Comitia packages coordination infrastructure; it does not eliminate the need for a recovery policy around unexpected lease expiry. Its measured storage overhead was less than one second per matched trial; model generation consumed minutes.

The earlier 90 short-task trials also found no quality advantage over ordinary worker/reviewer transport. On simple factual tasks all modes were correct, while the team workflows roughly doubled time and captured model cost. Larger tasks allow useful overlap, but extra roles and review still consume resources and may preserve mistakes. The new sequential control includes the same five stages and self-review, addressing the earlier lack of a self-review comparison.

## Scope and evidence

Each stage is a fresh model session. The sequential engineer receives accumulated earlier outputs; restart/resume means restoring the workflow from durable results, not resuming a native model session. The model sessions have no tools and follow a fixed role graph. The adapter executes generated files and supplies four public smoke-check results to the reviewer; eight checks remain hidden. This tests larger, multi-step work with real process restarts, not autonomous tool choice, repository navigation, production deployment, or arbitrary crash points. Deliberate SIGKILL barriers occur after model responses have returned, so their discarded usage can be counted. Mid-token cancellation, network partitions, and multi-host operation are not measured.

[Methodology and reproduction](../../evaluation/recovery-long/README.md) · [Official harness aggregate](../../evaluation/recovery-long/results.md) · [All original observations](../../evaluation/recovery-long/results.json) · [Clean-control results](../../evaluation/recovery-long/awake-control-results.md) · [Calibration evidence](../../evaluation/recovery/analysis.md) · [Earlier six-category evaluation](comitia-evaluation-report.md)

The extension recorded 23 trials: 18 main, 3 separate clean controls, and 2 calibration trials. Main plus control used 100 model-client calls and captured $6.3603 in list-price usage; the host-affected timeout has missing usage, so this is a lower bound. Calibration is separate. Eight grader/recovery tests pass, and official summaries were regenerated from saved observations.

Production Comitia code was unchanged. Reusable evaluation definitions, adapters, grader checks, recovery tests, analysis, and documentation were added locally; this report preserves the state at evaluation completion; the evaluation bundle was subsequently committed.
