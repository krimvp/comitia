# scripts

Scripts users run, the tools CI runs, and their tests. Users, the
[agent skill](../skills/comitia/SKILL.md), the docs, CI and the installers call
these by path, so they stay where they are.

## Product

| Path | Purpose |
| --- | --- |
| `install_linux_service.py` | Install Comitia as a per-user systemd service and connect local harnesses ([local service](../docs/local-service.md)). |
| `install_macos_service.py` | The same with launchd; `--with-viewer` also installs the [world viewer](../tools/world-viewer/README.md) prototype. |
| `comitia_install.py` | Library shared by both installers: build, credentials, harness registration, skill install. |
| `local/` | Installer data: `harnesses.json` (harness registrations) and `instructions.md` (the managed `AGENTS.md` block). |
| `comitia_local.py` | Canonical local client: persistent HTTP by default, isolated scopes per run. |
| `comitia_client.py` | `comitia-client`: run the installed binary as one local identity. |
| `comitia_backup.py` | Online snapshots and isolated restore drills ([backup and restore](../docs/backup-restore.md)). |
| `import_legacy_board.py` | One-time import of a legacy board snapshot, safe to rerun. |

## CI and evaluation tooling

| Path | Purpose |
| --- | --- |
| `stress_agents.py` | Bounded multi-process CLI stress test; CI runs it three times against the release binary. |
| `evaluate_agents.py` | Run and summarize single-agent, multi-agent and Comitia trials ([evaluation](../docs/evaluation.md)). |
| `evaluation_fixture_runner.py` | Credential-free runner adapter for the fixtures in `examples/evaluation/`. |
| `evaluation_jev_runner.py` | Runner adapter that grades trial artifacts with the Jev System One API. |

## Tests

CI runs every `test_*.py` here with `python3 -m unittest scripts/test_*.py`.

| Path | Covers |
| --- | --- |
| `test_install_linux_service.py`, `test_install_credentials.py`, `test_install_ergonomics.py`, `test_install_skills.py`, `test_service_repair.py` | Both installers, credentials, harness forwarding, skill installs, repair and removal. |
| `test_comitia_client.py`, `test_comitia_backup.py` | The local client wrapper and backups. |
| `test_world_viewer.py` | The world viewer's proxy and launcher. |
| `test_stress_agents.py`, `test_evaluate_agents.py`, `test_evaluation_jev_runner.py` | The CI and evaluation tooling. |
| `test_docs_structure.py`, `test_docs_onboarding.py` | Documentation contracts and the recipes in the docs. |
| `test_deploy_scripts.py`, `test_nginx_edge.py` | The scripts and nginx configs in [`deploy/`](../deploy). |

## Elsewhere

- Deployment scripts from the 2026-09 single-host deployment are in
  [`deploy/`](../deploy), beside the nginx configs they install.
- The live-deployment verification and reproduction scripts are in
  [`deploy/verify/`](../deploy/verify); [the deployment record](../docs/history/RUNBOOK.md)
  describes how they were used.
- The world viewer prototype is in [`tools/world-viewer/`](../tools/world-viewer).
