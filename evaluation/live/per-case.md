# Per-case real-model results (original as-run scores)

Original as-run scores before the separate score audit. See the main report for corrected comparisons. Every cell: five trials, same task and grader; quality is deterministic requirement compliance.

| Category / case | Mode | Quality | Passes | Total USD | Mean seconds | Input / output tokens |
|---|---|---:|---:|---:|---:|---:|
| Factual / low complexity / factual-basics | single-agent | 100.0% | 5/5 | 0.009135 | 4.86 | 1,600 / 1,507 |
| Factual / low complexity / factual-basics | multi-agent | 100.0% | 5/5 | 0.018846 | 8.49 | 3,686 / 3,032 |
| Factual / low complexity / factual-basics | comitia | 100.0% | 5/5 | 0.019823 | 9.18 | 3,703 / 3,224 |
| Coding / refactoring / stable-refactor | single-agent | 0.0% | 0/5 | 0.039720 | 13.44 | 2,160 / 7,512 |
| Coding / refactoring / stable-refactor | multi-agent | 100.0% | 5/5 | 0.095628 | 34.15 | 5,638 / 17,998 |
| Coding / refactoring / stable-refactor | comitia | 100.0% | 5/5 | 0.095340 | 34.72 | 5,580 / 17,952 |
| Debugging / ttl-cache-debug | single-agent | 0.0% | 0/5 | 0.130095 | 40.60 | 2,490 / 25,521 |
| Debugging / ttl-cache-debug | multi-agent | 100.0% | 5/5 | 0.258820 | 89.89 | 6,525 / 50,459 |
| Debugging / ttl-cache-debug | comitia | 100.0% | 5/5 | 0.224514 | 77.07 | 6,594 / 43,584 |
| Architecture / reasoning / outbox-architecture | single-agent | 88.6% | 1/5 | 0.180541 | 69.62 | 3,066 / 35,495 |
| Architecture / reasoning / outbox-architecture | multi-agent | 80.0% | 4/5 | 0.218484 | 102.65 | 5,014 / 42,694 |
| Architecture / reasoning / outbox-architecture | comitia | 60.0% | 3/5 | 0.149293 | 93.70 | 3,768 / 29,105 |
| Collaboration / orchestration / delegation-schedule | single-agent | 17.1% | 0/5 | 0.256825 | 88.32 | 2,455 / 50,874 |
| Collaboration / orchestration / delegation-schedule | multi-agent | 17.1% | 0/5 | 0.174998 | 136.74 | 2,798 / 34,440 |
| Collaboration / orchestration / delegation-schedule | comitia | 34.3% | 0/5 | 0.223188 | 143.28 | 3,203 / 43,997 |
| Communication heavy / incident-handoff | single-agent | 97.1% | 4/5 | 0.055763 | 22.77 | 3,308 / 10,491 |
| Communication heavy / incident-handoff | multi-agent | 100.0% | 5/5 | 0.125193 | 41.09 | 7,068 / 23,625 |
| Communication heavy / incident-handoff | comitia | 100.0% | 5/5 | 0.098014 | 30.31 | 7,039 / 18,195 |

## Failed checks

- delegation-schedule, comitia, repetition 0: all-tasks, workers, dependencies, file-conflict, optimal, stale-lease, handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--0--comitia/grader.json).
- delegation-schedule, multi-agent, repetition 0: handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--0--multi-agent/grader.json).
- delegation-schedule, single-agent, repetition 0: all-tasks, workers, dependencies, file-conflict, optimal, stale-lease, handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--0--single-agent/grader.json).
- delegation-schedule, comitia, repetition 1: handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--1--comitia/grader.json).
- delegation-schedule, multi-agent, repetition 1: all-tasks, workers, dependencies, file-conflict, optimal, stale-lease, handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--1--multi-agent/grader.json).
- delegation-schedule, single-agent, repetition 1: all-tasks, workers, dependencies, file-conflict, optimal, stale-lease, handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--1--single-agent/grader.json).
- delegation-schedule, comitia, repetition 2: handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--2--comitia/grader.json).
- delegation-schedule, multi-agent, repetition 2: all-tasks, workers, dependencies, file-conflict, optimal, stale-lease, handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--2--multi-agent/grader.json).
- delegation-schedule, single-agent, repetition 2: handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--2--single-agent/grader.json).
- delegation-schedule, comitia, repetition 3: all-tasks, workers, dependencies, file-conflict, optimal, stale-lease, handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--3--comitia/grader.json).
- delegation-schedule, multi-agent, repetition 3: all-tasks, workers, dependencies, file-conflict, optimal, stale-lease, handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--3--multi-agent/grader.json).
- delegation-schedule, single-agent, repetition 3: all-tasks, workers, dependencies, file-conflict, optimal, stale-lease, handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--3--single-agent/grader.json).
- delegation-schedule, comitia, repetition 4: all-tasks, workers, dependencies, file-conflict, optimal, stale-lease, handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--4--comitia/grader.json).
- delegation-schedule, multi-agent, repetition 4: all-tasks, workers, dependencies, file-conflict, optimal, stale-lease, handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--4--multi-agent/grader.json).
- delegation-schedule, single-agent, repetition 4: all-tasks, workers, dependencies, file-conflict, optimal, stale-lease, handoff. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/delegation-schedule--4--single-agent/grader.json).
- incident-handoff, single-agent, repetition 1: length. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/incident-handoff--1--single-agent/grader.json).
- outbox-architecture, single-agent, repetition 0: transaction. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/outbox-architecture--0--single-agent/grader.json).
- outbox-architecture, comitia, repetition 1: design, transaction, key, delivery, effects, calls, rollback. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/outbox-architecture--1--comitia/grader.json).
- outbox-architecture, single-agent, repetition 1: delivery. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/outbox-architecture--1--single-agent/grader.json).
- outbox-architecture, single-agent, repetition 2: transaction. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/outbox-architecture--2--single-agent/grader.json).
- outbox-architecture, comitia, repetition 3: design, transaction, key, delivery, effects, calls, rollback. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/outbox-architecture--3--comitia/grader.json).
- outbox-architecture, single-agent, repetition 3: transaction. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/outbox-architecture--3--single-agent/grader.json).
- outbox-architecture, multi-agent, repetition 4: design, transaction, key, delivery, effects, calls, rollback. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/outbox-architecture--4--multi-agent/grader.json).
- stable-refactor, single-agent, repetition 0: empty, duplicates, order, signed, immutable, randomized, linear-work. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/stable-refactor--0--single-agent/grader.json).
- stable-refactor, single-agent, repetition 1: empty, duplicates, order, signed, immutable, randomized, linear-work. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/stable-refactor--1--single-agent/grader.json).
- stable-refactor, single-agent, repetition 2: empty, duplicates, order, signed, immutable, randomized, linear-work. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/stable-refactor--2--single-agent/grader.json).
- stable-refactor, single-agent, repetition 3: empty, duplicates, order, signed, immutable, randomized, linear-work. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/stable-refactor--3--single-agent/grader.json).
- stable-refactor, single-agent, repetition 4: empty, duplicates, order, signed, immutable, randomized, linear-work. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/stable-refactor--4--single-agent/grader.json).
- ttl-cache-debug, single-agent, repetition 0: miss-hit, falsy, boundary, no-sliding, nonpositive-ttl, loader-failure, isolation. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/ttl-cache-debug--0--single-agent/grader.json).
- ttl-cache-debug, single-agent, repetition 1: miss-hit, falsy, boundary, no-sliding, nonpositive-ttl, loader-failure, isolation. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/ttl-cache-debug--1--single-agent/grader.json).
- ttl-cache-debug, single-agent, repetition 2: miss-hit, falsy, boundary, no-sliding, nonpositive-ttl, loader-failure, isolation. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/ttl-cache-debug--2--single-agent/grader.json).
- ttl-cache-debug, single-agent, repetition 3: miss-hit, falsy, boundary, no-sliding, nonpositive-ttl, loader-failure, isolation. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/ttl-cache-debug--3--single-agent/grader.json).
- ttl-cache-debug, single-agent, repetition 4: miss-hit, falsy, boundary, no-sliding, nonpositive-ttl, loader-failure, isolation. [Evidence](/Users/taniaprado/.codex/.chatgpt-projects/g-p-6a9c9bdc9dec819188ca312219961831/comitia-eval/evaluation/live/artifacts/ttl-cache-debug--4--single-agent/grader.json).

## Worker/reviewer changes

multi-agent: 11 improved, 0 worsened, 17 unchanged.
comitia: 14 improved, 0 worsened, 12 unchanged.
- delegation-schedule, multi-agent, repetition 0: 0.0% → 85.7%.
- delegation-schedule, comitia, repetition 1: 0.0% → 85.7%.
- delegation-schedule, comitia, repetition 2: 0.0% → 85.7%.
- incident-handoff, comitia, repetition 1: 85.7% → 100.0%.
- incident-handoff, comitia, repetition 2: 85.7% → 100.0%.
- stable-refactor, comitia, repetition 0: 0.0% → 100.0%.
- stable-refactor, multi-agent, repetition 0: 0.0% → 100.0%.
- stable-refactor, comitia, repetition 1: 0.0% → 100.0%.
- stable-refactor, multi-agent, repetition 1: 0.0% → 100.0%.
- stable-refactor, comitia, repetition 2: 0.0% → 100.0%.
- stable-refactor, multi-agent, repetition 2: 0.0% → 100.0%.
- stable-refactor, comitia, repetition 3: 0.0% → 100.0%.
- stable-refactor, multi-agent, repetition 3: 0.0% → 100.0%.
- stable-refactor, comitia, repetition 4: 0.0% → 100.0%.
- stable-refactor, multi-agent, repetition 4: 0.0% → 100.0%.
- ttl-cache-debug, comitia, repetition 0: 0.0% → 100.0%.
- ttl-cache-debug, multi-agent, repetition 0: 0.0% → 100.0%.
- ttl-cache-debug, comitia, repetition 1: 0.0% → 100.0%.
- ttl-cache-debug, multi-agent, repetition 1: 0.0% → 100.0%.
- ttl-cache-debug, comitia, repetition 2: 0.0% → 100.0%.
- ttl-cache-debug, multi-agent, repetition 2: 0.0% → 100.0%.
- ttl-cache-debug, comitia, repetition 3: 0.0% → 100.0%.
- ttl-cache-debug, multi-agent, repetition 3: 0.0% → 100.0%.
- ttl-cache-debug, comitia, repetition 4: 0.0% → 100.0%.
- ttl-cache-debug, multi-agent, repetition 4: 0.0% → 100.0%.

## Post-hoc format sensitivity

Primary scores are unchanged. This secondary analysis extracts the first JSON object and applies the same checks, allowing trailing commentary.
single-agent: 50.5% primary vs 94.3% extracted-JSON quality; 14 affected runs.
multi-agent: 99.4% primary vs 99.4% extracted-JSON quality; 0 affected runs.
comitia: 98.9% primary vs 98.9% extracted-JSON quality; 0 affected runs.
