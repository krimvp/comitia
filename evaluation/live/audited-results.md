# Agent evaluation

| Mode | Quality | Passes | Known cost | Cost coverage | Mean wall time | Quality / USD |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| single-agent | 52.4% | 14/30 | $0.672079 | 100.0% | 39.936s | 23.382 |
| multi-agent | 83.3% | 25/30 | $0.778026 | 83.3% | 68.835s | n/a |
| comitia | 83.3% | 25/30 | $0.775481 | 83.3% | 64.710s | n/a |

## Paired differences from single-agent

| Mode | Mean quality difference | Quality wins | Mean cost difference |
| --- | ---: | ---: | ---: |
| multi-agent | +31.0 pp | 13/30 | n/a |
| comitia | +31.0 pp | 12/30 | n/a |

Quality includes zero scores for failed runs. Cost efficiency is omitted unless every run in that mode has complete cost capture.
