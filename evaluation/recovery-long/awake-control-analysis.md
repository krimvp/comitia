# Extended recovery evaluation results

3 of 3 trials recorded. 1 repetitions per fault condition and mode. One multi-file engineering workload.

| Mode | Checks passed | Full passes | Completed | Captured cost | Cost coverage | Tokens in / out | Mean elapsed | Mean storage |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| single-agent | 100.0% | 1/1 | 1/1 | $0.3028 | 1/1 | 18,518 / 56,859 | 442.0s | 0.018s |
| multi-agent | 91.7% | 0/1 | 1/1 | $0.3304 | 1/1 | 16,735 / 62,739 | 439.6s | 0.019s |
| comitia | 83.3% | 0/1 | 1/1 | $0.3114 | 1/1 | 16,978 / 58,890 | 404.4s | 0.698s |

## Individual trials

| Fault | Rep | Mode | Checks | Status | Seconds | Captured cost | Recovery verified |
|---|---:|---|---:|---|---:|---:|---|
| none | 0 | comitia | 83.3% | completed | 404.4 | $0.3114 | None |
| none | 0 | multi-agent | 91.7% | completed | 439.6 | $0.3304 | None |
| none | 0 | single-agent | 100.0% | completed | 442.0 | $0.3028 | None |

## Failure evidence

- order-system-clean / 0 / comitia: {"ledger_validation": "AssertionError: expected ValueError", "handoff": "AssertionError: ('database',)"}
- order-system-clean / 0 / multi-agent: {"handoff": "AssertionError: word count 226"}

Recovery verification requires a new engine PID, recovered stages, and an unchanged authoritative result after retry/stale submission. A task can recover correctly and still fail behavioral checks later. Missing model timeout usage makes cost a lower bound. Full methodological limits are in README.md.
