# Agent evaluation

| Mode | Quality | Passes | Known cost | Cost coverage | Mean wall time | Quality / USD |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| single-agent | 100.0% | 1/1 | $0.302813 | 100.0% | 442.038s | 3.302 |
| multi-agent | 91.7% | 0/1 | $0.330430 | 100.0% | 439.612s | 2.774 |
| comitia | 83.3% | 0/1 | $0.311428 | 100.0% | 404.441s | 2.676 |

## Paired differences from single-agent

| Mode | Mean quality difference | Quality wins | Mean cost difference |
| --- | ---: | ---: | ---: |
| multi-agent | -8.3 pp | 0/1 | $+0.027617 |
| comitia | -16.7 pp | 0/1 | $+0.008615 |

Quality includes zero scores for failed runs. Cost efficiency is omitted unless every run in that mode has complete cost capture.
