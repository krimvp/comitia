# Extended recovery evaluation results

18 of 18 trials recorded. Two repetitions per fault condition and mode. One multi-file engineering workload, three fault profiles.

| Mode | Checks passed | Full passes | Completed | Captured cost | Cost coverage | Tokens in / out | Mean elapsed | Mean storage |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| single-agent | 73.6% | 0/6 | 5/6 | $1.8856 | 5/6 | 128,637 / 351,400 | 572.4s | 0.022s |
| multi-agent | 59.7% | 0/6 | 4/6 | $1.6838 | 6/6 | 118,533 / 313,048 | 387.4s | 0.018s |
| comitia | 76.4% | 1/6 | 5/6 | $1.8462 | 6/6 | 94,237 / 350,391 | 397.1s | 0.809s |

## Individual trials

| Fault | Rep | Mode | Checks | Status | Seconds | Captured cost | Recovery verified |
|---|---:|---|---:|---|---:|---:|---|
| none | 0 | comitia | 0.0% | failed | 40.1 | $0.0134 | None |
| none | 0 | multi-agent | 0.0% | failed | 50.8 | $0.0146 | None |
| none | 0 | single-agent | 0.0% | timeout | 796.8 | $0.1391 | None |
| none | 1 | comitia | 91.7% | completed | 490.8 | $0.3612 | None |
| none | 1 | multi-agent | 91.7% | completed | 406.9 | $0.3715 | None |
| none | 1 | single-agent | 91.7% | completed | 422.9 | $0.3075 | None |
| handoff | 0 | comitia | 91.7% | completed | 389.7 | $0.3147 | True |
| handoff | 0 | multi-agent | 0.0% | failed | 366.1 | $0.1819 | False |
| handoff | 0 | single-agent | 91.7% | completed | 525.5 | $0.3464 | True |
| handoff | 1 | comitia | 100.0% | completed | 448.5 | $0.3472 | True |
| handoff | 1 | multi-agent | 91.7% | completed | 412.0 | $0.3281 | True |
| handoff | 1 | single-agent | 91.7% | completed | 417.7 | $0.3033 | True |
| lease | 0 | comitia | 83.3% | completed | 479.9 | $0.4126 | True |
| lease | 0 | multi-agent | 83.3% | completed | 511.6 | $0.3858 | True |
| lease | 0 | single-agent | 75.0% | completed | 633.1 | $0.3999 | True |
| lease | 1 | comitia | 91.7% | completed | 533.8 | $0.3971 | True |
| lease | 1 | multi-agent | 91.7% | completed | 577.1 | $0.4018 | True |
| lease | 1 | single-agent | 91.7% | completed | 638.4 | $0.3893 | True |

## Failure evidence

- order-system-clean / 0 / comitia: "RuntimeError: current completion rejected"
- order-system-clean / 0 / multi-agent: "RuntimeError: current completion rejected"
- order-system-clean / 0 / single-agent: "TimeoutError: model call exceeded 600s"
- order-system-clean / 1 / comitia: {"handoff": "AssertionError: word count 324"}
- order-system-clean / 1 / multi-agent: {"handoff": "AssertionError: word count 351"}
- order-system-clean / 1 / single-agent: {"handoff": "AssertionError: word count 251"}
- order-system-handoff-crash / 0 / comitia: {"handoff": "AssertionError: word count 344"}
- order-system-handoff-crash / 0 / multi-agent: "RuntimeError: current completion rejected"
- order-system-handoff-crash / 0 / single-agent: {"handoff": "AssertionError: word count 373"}
- order-system-handoff-crash / 1 / multi-agent: {"handoff": "AssertionError: word count 249"}
- order-system-handoff-crash / 1 / single-agent: {"handoff": "AssertionError: word count 223"}
- order-system-lease-takeover / 0 / comitia: {"ledger_validation": "AssertionError: expected ValueError", "handoff": "AssertionError: word count 325"}
- order-system-lease-takeover / 0 / multi-agent: {"ledger_validation": "AssertionError: expected ValueError", "handoff": "AssertionError: word count 231"}
- order-system-lease-takeover / 0 / single-agent: {"outbox_fencing": "AssertionError: ", "outbox_dedupe_snapshot": "AssertionError: ", "handoff": "AssertionError: word count 247"}
- order-system-lease-takeover / 1 / comitia: {"handoff": "AssertionError: word count 247"}
- order-system-lease-takeover / 1 / multi-agent: {"handoff": "AssertionError: word count 407"}
- order-system-lease-takeover / 1 / single-agent: {"handoff": "AssertionError: word count 315"}

Recovery verification requires a new engine PID, recovered stages, and an unchanged authoritative result after retry/stale submission. A task can recover correctly and still fail behavioral checks later. Missing model timeout usage makes cost a lower bound. Full methodological limits are in README.md.
