# Agent evaluation

| Mode | Quality | Passes | Known cost | Cost coverage | Mean wall time | Quality / USD |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| single-agent | 50.5% | 10/30 | $0.672079 | 100.0% | 39.936s | 22.531 |
| multi-agent | 82.9% | 24/30 | $0.778026 | 83.3% | 68.835s | n/a |
| comitia | 82.4% | 23/30 | $0.775481 | 83.3% | 64.710s | n/a |

## Paired differences from single-agent

| Mode | Mean quality difference | Quality wins | Mean cost difference |
| --- | ---: | ---: | ---: |
| multi-agent | +32.4 pp | 16/30 | n/a |
| comitia | +31.9 pp | 14/30 | n/a |

Quality includes zero scores for failed runs. Cost efficiency is omitted unless every run in that mode has complete cost capture.
