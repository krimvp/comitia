# Agent evaluation

| Mode | Quality | Passes | Known cost | Cost coverage | Mean wall time | Quality / USD |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| single-agent | 81.7% | 2/4 | $0.007360 | 100.0% | 0.077s | 443.841 |
| multi-agent | 91.7% | 4/4 | $0.009300 | 100.0% | 0.076s | 394.265 |
| comitia | 98.3% | 4/4 | $0.011240 | 100.0% | 0.077s | 349.941 |

## Paired differences from single-agent

| Mode | Mean quality difference | Quality wins | Mean cost difference |
| --- | ---: | ---: | ---: |
| multi-agent | +10.0 pp | 4/4 | $+0.000485 |
| comitia | +16.7 pp | 4/4 | $+0.000970 |

Quality includes zero scores for failed runs. Cost efficiency is omitted unless every run in that mode has complete cost capture.
