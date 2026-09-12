# Recorded evaluation bundle

- [Six-category, 90-trial report](../docs/evaluations/comitia-evaluation-report.md)
- [Longer engineering and recovery report](../docs/evaluations/comitia-recovery-evaluation-report.md)
- `live/`: short-task matrix, frozen observations, raw model outputs, audited score corrections.
- `recovery/`: two calibration trials, stopped when the time budget was increased.
- `recovery-long/`: 18 main trials and three separately labeled clean controls, with failures and host-sleep evidence retained.
- `inspector-live/`: separate live demonstration adapter; its database, credentials and changing output are excluded.

Recorded JSON retains original absolute artifact paths and execution hashes as provenance. It is an experiment snapshot, not a portable checkpoint for resuming paid calls. Reports above use repository-relative links. To run a new experiment, use a fresh directory and regenerate its spec with the local artifact path; do not overwrite these observations. The model CLI requires your own authentication. No provider credentials are included.

Validation without model calls:

```sh
python3 -m unittest scripts/test_evaluate_agents.py
python3 -m unittest discover -s evaluation/live -p 'test_*.py'
python3 -m unittest discover -s evaluation/recovery-long -p 'test_*.py'
```

Build `bin/comitia` using the repository README before the process-level recovery tests. The original observations, including failures, remain unchanged; post-hoc diagnostics are labeled separately.
