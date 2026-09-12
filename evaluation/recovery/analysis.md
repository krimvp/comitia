# Extended recovery evaluation results

2 of 18 trials recorded. Two repetitions per fault condition and mode. One multi-file engineering workload, three fault profiles.

| Mode | Checks passed | Full passes | Completed | Captured cost | Cost coverage | Tokens in / out | Mean elapsed | Mean storage |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| single-agent | 100.0% | 1/1 | 1/1 | $0.3622 | 1/1 | 22,916 / 67,865 | 601.8s | 0.017s |
| multi-agent | 83.3% | 0/1 | 1/1 | $0.3322 | 1/1 | 18,469 / 62,754 | 501.5s | 0.019s |

## Individual trials

| Fault | Rep | Mode | Checks | Status | Seconds | Captured cost | Recovery verified |
|---|---:|---|---:|---|---:|---:|---|
| none | 0 | multi-agent | 83.3% | completed | 501.5 | $0.3322 | None |
| none | 0 | single-agent | 100.0% | completed | 601.8 | $0.3622 | None |

## Failure evidence

- order-system-clean / 0 / multi-agent: {"service_rollback": "AssertionError: ", "handoff": "AssertionError: word count 233"}

Recovery verification requires a new engine PID, recovered stages, and an unchanged authoritative result after retry/stale submission. A task can recover correctly and still fail behavioral checks later. Missing model timeout usage makes cost a lower bound. Full methodological limits are in README.md.
