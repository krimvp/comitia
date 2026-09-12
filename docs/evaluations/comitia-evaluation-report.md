# Comitia evaluation — September 11, 2026

Executed against latest remote `main`, commit `570583b0d6cd2ce8ba8e05f1cce6d61b710db611`, in an isolated checkout. **90 real-model trials: six categories × five repetitions × three modes**, using pinned `claude-haiku-4-5-20251001` via Claude Code 2.1.263. Bundled synthetic fixture results are excluded.

**Finding:** Comitia scored 83.3% and ordinary worker/reviewer scored 83.3% on audited checks. Their mean difference was +0.0 percentage points. Read these alongside the content sensitivity analysis: response formatting and timeouts materially affect the comparison.


## Aggregate results

“Checks” measures fixed requirement compliance, including JSON response format. “Content” is a post-hoc sensitivity analysis that extracts JSON from surrounding prose before applying the same checks. Both correct an overly literal match on equivalent architecture entity names and exclude the ambiguous handoff-field check uniformly. Scheduling retains its six other checks. Original as-run scores and every correction remain available.

| Mode | Checks | Content checks | Full passes | Completed | Captured model cost* | Cost coverage | Input / output tokens* | Mean trial time |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Single agent | 52.4% | 97.9% | 14/30 | 30/30 | $0.6721 | 100.0% | 15,079 / 131,400 | 39.9s |
| Worker + reviewer | 83.3% | 83.3% | 25/30 | 25/30 | $0.8920 | 83.3% | 30,729 / 172,248 | 68.8s |
| Comitia worker + reviewer | 83.3% | 83.3% | 25/30 | 25/30 | $0.8102 | 83.3% | 29,887 / 156,057 | 64.7s |

*Captured costs/tokens include returned worker and reviewer usage, including reported thinking and auxiliary usage. Timed-out calls may have unreported usage; these totals are lower bounds where coverage is incomplete. Captured cost includes returned usage from incomplete trials and can exceed the standard harness completed-cost subtotal. Dollar amounts are standard [Anthropic list-price equivalents](https://platform.claude.com/docs/en/about-claude/pricing), not subscription invoices. Local compute cost is excluded equally. The harness suppresses cost efficiency when any trial has incomplete cost capture.

## Per-category results

Each cell is **requirement-check score / content-check score**, averaged over five trials. All categories have equal weight.

| Task category | Single agent | Worker + reviewer | Comitia |
|---|---:|---:|---:|
| Factual / low complexity — `factual-basics` | 100.0% / 100.0% | 100.0% / 100.0% | 100.0% / 100.0% |
| Coding / refactoring — `stable-refactor` | 0.0% / 100.0% | 100.0% / 100.0% | 100.0% / 100.0% |
| Debugging — `ttl-cache-debug` | 0.0% / 100.0% | 100.0% / 100.0% | 100.0% / 100.0% |
| Architecture / reasoning — `outbox-architecture` | 97.1% / 97.1% | 80.0% / 80.0% | 60.0% / 60.0% |
| Collaboration / orchestration — `delegation-schedule` | 20.0% / 93.3% | 20.0% / 20.0% | 40.0% / 40.0% |
| Communication heavy — `incident-handoff` | 97.1% / 97.1% | 100.0% / 100.0% | 100.0% / 100.0% |

## Detailed per-task measurements

Cost is all captured usage, including partial usage from incomplete trials, not a completed-only mean.

| Case | Mode | Passes / completed / trials | Captured USD | Mean seconds | Input / output tokens |
|---|---|---:|---:|---:|---:|
| factual-basics | Single agent | 5 / 5 / 5 | $0.009135 | 4.86 | 1,600 / 1,507 |
| factual-basics | Worker + reviewer | 5 / 5 / 5 | $0.018846 | 8.49 | 3,686 / 3,032 |
| factual-basics | Comitia worker + reviewer | 5 / 5 / 5 | $0.019823 | 9.18 | 3,703 / 3,224 |
| stable-refactor | Single agent | 0 / 5 / 5 | $0.039720 | 13.44 | 2,160 / 7,512 |
| stable-refactor | Worker + reviewer | 5 / 5 / 5 | $0.095628 | 34.15 | 5,638 / 17,998 |
| stable-refactor | Comitia worker + reviewer | 5 / 5 / 5 | $0.095340 | 34.72 | 5,580 / 17,952 |
| ttl-cache-debug | Single agent | 0 / 5 / 5 | $0.130095 | 40.60 | 2,490 / 25,521 |
| ttl-cache-debug | Worker + reviewer | 5 / 5 / 5 | $0.258820 | 89.89 | 6,525 / 50,459 |
| ttl-cache-debug | Comitia worker + reviewer | 5 / 5 / 5 | $0.224514 | 77.07 | 6,594 / 43,584 |
| outbox-architecture | Single agent | 4 / 5 / 5 | $0.180541 | 69.62 | 3,066 / 35,495 |
| outbox-architecture | Worker + reviewer | 4 / 4 / 5 | $0.218484 | 102.65 | 5,014 / 42,694 |
| outbox-architecture | Comitia worker + reviewer | 3 / 3 / 5 | $0.149293 | 93.70 | 3,768 / 29,105 |
| delegation-schedule | Single agent | 1 / 5 / 5 | $0.256825 | 88.32 | 2,455 / 50,874 |
| delegation-schedule | Worker + reviewer | 1 / 1 / 5 | $0.174998 | 136.74 | 2,798 / 34,440 |
| delegation-schedule | Comitia worker + reviewer | 2 / 2 / 5 | $0.223188 | 143.28 | 3,203 / 43,997 |
| incident-handoff | Single agent | 4 / 5 / 5 | $0.055763 | 22.77 | 3,308 / 10,491 |
| incident-handoff | Worker + reviewer | 5 / 5 / 5 | $0.125193 | 41.09 | 7,068 / 23,625 |
| incident-handoff | Comitia worker + reviewer | 5 / 5 / 5 | $0.098014 | 30.31 | 7,039 / 18,195 |

## Paired differences and coordination overhead

Bootstrap intervals describe this small fixed task set; they do not establish general performance across repositories or models. The CLI does not expose a sampling seed.

- Comitia worker + reviewer vs single: +31.0 percentage points in requirement checks; bootstrap 95% interval [+11.4, +51.0]. Wins: 12/30; ties: 16. Paired cost difference: unavailable because of incomplete usage.
- Worker + reviewer vs single: +31.0 percentage points in requirement checks; bootstrap 95% interval [+10.0, +51.0]. Wins: 13/30; ties: 15. Paired cost difference: unavailable because of incomplete usage.
- Post-hoc content sensitivity, Comitia worker + reviewer vs single: -14.6 points; 95% interval [-27.9, -3.2]. This retains zero scores for timeouts.
- Post-hoc content sensitivity, Worker + reviewer vs single: -14.6 points; 95% interval [-28.6, -2.4]. This retains zero scores for timeouts.
- Comitia vs the same worker/reviewer workflow without Comitia: +0.0 points, 95% interval [-16.7, +16.7]; paired wall-time difference -4.12s. Paired cost difference suppressed because of incomplete usage.
- Actual Comitia operations: 441, with 0.226s mean coordination-command time per Comitia trial (including setup-only failed trials). The adapter hands the reviewer the exact message body in both modes, so Comitia adds no model-facing text in this experiment. Quality/latency variation between these modes is not evidence that the transport changed reasoning.

The matrix recorded 5,204.42 seconds of trial wall time (86.74 minutes), with 144 model-client invocations: 134 returned responses and 10 timed out. Returned output thinking-token percentages were Single agent 95.0%, Worker + reviewer 93.9%, Comitia worker + reviewer 93.6%. These exclude unavailable timeout usage. 6 timeouts occurred in worker calls and 4 in reviewer calls. 2 reviewer timeouts followed an already-correct worker draft. See [timeout inspection](../../evaluation/live/timeout-audit.json) and [client usage detail](../../evaluation/live/client-metrics.json).

## What reviewers changed

- Worker + reviewer, audited: 11 improved, 0 worsened, 14 tied among 25 returned final answers.
- Comitia worker + reviewer, audited: 14 improved, 0 worsened, 11 tied among 25 returned final answers.
- Worker + reviewer, content-sensitivity: 1 improved, 0 worsened, 24 tied among 25 returned final answers.
- Comitia worker + reviewer, content-sensitivity: 3 improved, 0 worsened, 22 tied among 25 returned final answers.

## Failures inspected

- `delegation-schedule`, comitia, repetition 0: Model client exceeded its 100-second call limit; usage incomplete. This is not a demonstrated Comitia storage failure. [Trace](../../evaluation/live/artifacts/delegation-schedule--0--comitia/events.json).
- `delegation-schedule`, single-agent, repetition 0: Response format/parser failure: JSONDecodeError: Extra data: line 14 column 1 (char 391) [Artifact](../../evaluation/live/artifacts/delegation-schedule--0--single-agent/answer.txt).
- `delegation-schedule`, multi-agent, repetition 1: Model client exceeded its 100-second call limit; usage incomplete. This is not a demonstrated Comitia storage failure. [Trace](../../evaluation/live/artifacts/delegation-schedule--1--multi-agent/events.json).
- `delegation-schedule`, single-agent, repetition 1: Response format/parser failure: JSONDecodeError: Extra data: line 14 column 1 (char 390) [Artifact](../../evaluation/live/artifacts/delegation-schedule--1--single-agent/answer.txt).
- `delegation-schedule`, multi-agent, repetition 2: Model client exceeded its 100-second call limit; usage incomplete. This is not a demonstrated Comitia storage failure. [Trace](../../evaluation/live/artifacts/delegation-schedule--2--multi-agent/events.json).
- `delegation-schedule`, comitia, repetition 3: Model client exceeded its 100-second call limit; usage incomplete. This is not a demonstrated Comitia storage failure. [Trace](../../evaluation/live/artifacts/delegation-schedule--3--comitia/events.json).
- `delegation-schedule`, multi-agent, repetition 3: Model client exceeded its 100-second call limit; usage incomplete. This is not a demonstrated Comitia storage failure. [Trace](../../evaluation/live/artifacts/delegation-schedule--3--multi-agent/events.json).
- `delegation-schedule`, single-agent, repetition 3: Response format/parser failure: JSONDecodeError: Extra data: line 14 column 1 (char 394) [Artifact](../../evaluation/live/artifacts/delegation-schedule--3--single-agent/answer.txt).
- `delegation-schedule`, comitia, repetition 4: Model client exceeded its 100-second call limit; usage incomplete. This is not a demonstrated Comitia storage failure. [Trace](../../evaluation/live/artifacts/delegation-schedule--4--comitia/events.json).
- `delegation-schedule`, multi-agent, repetition 4: Model client exceeded its 100-second call limit; usage incomplete. This is not a demonstrated Comitia storage failure. [Trace](../../evaluation/live/artifacts/delegation-schedule--4--multi-agent/events.json).
- `delegation-schedule`, single-agent, repetition 4: Response format/parser failure: JSONDecodeError: Extra data: line 14 column 1 (char 380) [Artifact](../../evaluation/live/artifacts/delegation-schedule--4--single-agent/answer.txt).
- `incident-handoff`, single-agent, repetition 1: Failed checks: length [Artifact](../../evaluation/live/artifacts/incident-handoff--1--single-agent/answer.txt).
- `outbox-architecture`, comitia, repetition 1: Model client exceeded its 100-second call limit; usage incomplete. This is not a demonstrated Comitia storage failure. [Trace](../../evaluation/live/artifacts/outbox-architecture--1--comitia/events.json).
- `outbox-architecture`, single-agent, repetition 1: Failed checks: delivery [Artifact](../../evaluation/live/artifacts/outbox-architecture--1--single-agent/answer.txt).
- `outbox-architecture`, comitia, repetition 3: Model client exceeded its 100-second call limit; usage incomplete. This is not a demonstrated Comitia storage failure. [Trace](../../evaluation/live/artifacts/outbox-architecture--3--comitia/events.json).
- `outbox-architecture`, multi-agent, repetition 4: Model client exceeded its 100-second call limit; usage incomplete. This is not a demonstrated Comitia storage failure. [Trace](../../evaluation/live/artifacts/outbox-architecture--4--multi-agent/events.json).
- `stable-refactor`, single-agent, repetition 0: Response format/parser failure: JSONDecodeError: Extra data: line 4 column 1 (char 424) [Artifact](../../evaluation/live/artifacts/stable-refactor--0--single-agent/answer.txt).
- `stable-refactor`, single-agent, repetition 1: Response format/parser failure: JSONDecodeError: Extra data: line 2 column 1 (char 350) [Artifact](../../evaluation/live/artifacts/stable-refactor--1--single-agent/answer.txt).
- `stable-refactor`, single-agent, repetition 2: Response format/parser failure: JSONDecodeError: Extra data: line 2 column 1 (char 317) [Artifact](../../evaluation/live/artifacts/stable-refactor--2--single-agent/answer.txt).
- `stable-refactor`, single-agent, repetition 3: Response format/parser failure: JSONDecodeError: Extra data: line 2 column 1 (char 298) [Artifact](../../evaluation/live/artifacts/stable-refactor--3--single-agent/answer.txt).
- `stable-refactor`, single-agent, repetition 4: Response format/parser failure: JSONDecodeError: Extra data: line 2 column 1 (char 239) [Artifact](../../evaluation/live/artifacts/stable-refactor--4--single-agent/answer.txt).
- `ttl-cache-debug`, single-agent, repetition 0: Response format/parser failure: JSONDecodeError: Extra data: line 2 column 1 (char 416) [Artifact](../../evaluation/live/artifacts/ttl-cache-debug--0--single-agent/answer.txt).
- `ttl-cache-debug`, single-agent, repetition 1: Response format/parser failure: JSONDecodeError: Extra data: line 2 column 1 (char 393) [Artifact](../../evaluation/live/artifacts/ttl-cache-debug--1--single-agent/answer.txt).
- `ttl-cache-debug`, single-agent, repetition 2: Response format/parser failure: JSONDecodeError: Extra data: line 2 column 1 (char 408) [Artifact](../../evaluation/live/artifacts/ttl-cache-debug--2--single-agent/answer.txt).
- `ttl-cache-debug`, single-agent, repetition 3: Response format/parser failure: JSONDecodeError: Extra data: line 4 column 1 (char 438) [Artifact](../../evaluation/live/artifacts/ttl-cache-debug--3--single-agent/answer.txt).
- `ttl-cache-debug`, single-agent, repetition 4: Response format/parser failure: JSONDecodeError: Extra data: line 2 column 1 (char 411) [Artifact](../../evaluation/live/artifacts/ttl-cache-debug--4--single-agent/answer.txt).

Additional manual observations are in [failure inspection notes](../../results/manual-audit-notes.md). Those notes distinguish correct code in a malformed response, the grader capitalization defect, genuinely overlapping file schedules, ambiguous handoffs excluded from audited numeric comparisons, and overpromising language missed by automatic communication checks.

## Existing harness and reliability checks

- All 79 Go tests passed.
- All 12 evaluator tests passed after fixing the fixture runner executable bit; before the fix, the documented fixture invocation produced 12 permission-denied harness errors.
- All 17 independent grader/audit reference and negative-control tests passed.
- Existing process stress harness passed with 12 workers, 1,200 writes, 120 deduplicated retries, 1,200 replayed messages, exactly one winner among 12 competing claims, one expired-lease recovery and rejection of a stale completion. Runtime: 4.920 seconds. These are subprocess clients, not LLM agents.

## Interpretation and limits

The extra reviewer can repair response formatting and some scheduling mistakes. That benefit is shared by the ordinary worker/reviewer baseline. A fixed durable transport does not itself make the same model prompts reason better. Comitia’s reliability tests demonstrate durable replay, exclusivity and stale-lease handling; this experiment does not estimate their economic benefit under production crashes or competing autonomous agents.

For small factual and already-correct answers, the extra review workflow consumes more model calls. Comitia adds a small local command overhead on top. For coordination-sensitive work, Comitia provides verified durable mechanisms, but choosing when and how to use them needs a separate dynamic-agent or fault-injection comparison. All tasks here are bounded, tools are disabled for models, and the adapter performs coordination. Architecture and communication scores are structured checks, not holistic expert judgments. There is no equal-token-budget or single-agent self-review control; benefits of an extra call cannot establish that separate agent identities are necessary. Some check failures arise from strict formatting. The ambiguous schema/value handoff example remains in original scores but is excluded from audited scores for every mode.

One client capability probe and one Comitia integration pilot were excluded from the 90 trials; their returned list-price equivalents were $0.002358 and $0.003686 respectively. No real matrix trial was discarded or silently retried.

## Reproducibility and changes

- [Method, prompts, pricing and execution contract](../../evaluation/live/README.md).
- [Audited results JSON](../../evaluation/live/audited-results.json); [original as-run JSON](../../evaluation/live/results.json); [content sensitivity JSON](../../evaluation/live/content-sensitivity-results.json).
- [Score corrections](../../evaluation/live/score-corrections.json); [environment and hashes](../../results/provenance.json).
- Only the supplied fixture runner executable bit changed. New adapter, cases, graders and reports are isolated in this checkout. This report preserves the state at evaluation completion; the evaluation bundle was subsequently committed, and the old local checkout and synced sources were untouched.
