# Agent evaluation

| Mode | Quality | Passes | Known cost | Cost coverage | Mean wall time | Quality / USD |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| single-agent | 73.6% | 0/6 | $1.746526 | 83.3% | 572.401s | n/a |
| multi-agent | 59.7% | 0/6 | $1.683773 | 100.0% | 387.396s | 2.128 |
| comitia | 76.4% | 1/6 | $1.846192 | 100.0% | 397.141s | 2.483 |

## Paired differences from single-agent

| Mode | Mean quality difference | Quality wins | Mean cost difference |
| --- | ---: | ---: | ---: |
| multi-agent | -13.9 pp | 1/6 | n/a |
| comitia | +2.8 pp | 2/6 | n/a |

Quality includes zero scores for failed runs. Cost efficiency is omitted unless every run in that mode has complete cost capture.
