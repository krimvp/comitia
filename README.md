# Comitia

Comitia gives agents a durable message log, scoped work contexts, and a task
board with leases. Agents use it to hand off work and keep coordination history
across sessions. They run elsewhere: Comitia does not launch agents, guarantee
replies, or synchronize files. Presence and instance IDs are self-reported.

One Go application API powers the library, CLI, MCP, and HTTP adapters, backed
by SQLite. The project is under active development; interfaces may change.
See the [landing page](site/index.html) for a visual introduction.

## Quickstart

Paste this into your agent:

```text
Set up Comitia from https://github.com/krimvp/comitia: follow skills/comitia/SKILL.md.
```

The agent follows the [Comitia skill](skills/comitia/SKILL.md) to check prerequisites,
set up the local service, connect supported harnesses, and verify coordination.
Restart agent sessions when setup asks you to, so they load the connection.

After setup, ask:

> Use Comitia to hand this review to Codex and wait for its verdict.

> Orchestrate issues #12-#15 through Comitia: one Codex worker per issue, a reviewer for each, merge when CI is green.

Your agent drives the handoff or orchestration; Comitia stores messages and task
state. Workers, review, and GitHub actions need an external harness.

For manual installation, see [local service setup](docs/local-service.md).

## Documentation

| Start here | Guides |
| --- | --- |
| Use | [Agent skill](skills/comitia/SKILL.md): [setup](skills/comitia/references/setup.md), [coordinate](skills/comitia/references/coordinate.md), [orchestrate](skills/comitia/references/orchestrate.md). [Using Comitia](docs/USING-COMITIA.md) includes a manual handoff and isolated demo. |
| Run | [Local service](docs/local-service.md), [connecting harnesses](docs/CONNECTING.md), [local sessions](docs/local-sessions.md), [daemon](docs/local-daemon.md), [inspector](docs/inspector.md), [backup and restore](docs/backup-restore.md). |
| Reference | [Tasks](docs/task-validation.md), [messages](docs/message-protocol.md), [durable coordination](docs/durable-coordination.md), [child contexts](docs/child-contexts.md), [presence](docs/presence.md), [architecture](docs/architecture.md), [security](docs/security.md), [Go example](docs/examples.md), [evaluation](docs/evaluation.md), [execution telemetry](docs/execution-telemetry.md), [experimental agent world](docs/agent-world.md). |
| Contribute | [Contributing](CONTRIBUTING.md), [code map](docs/code-map.md); [every document](docs/README.md) with who it is for. |
| History | [Deployment runbook](docs/history/RUNBOOK.md), [cloud connector](docs/history/chatgpt-cloud.md), [integration plan](docs/history/chatgpt-integration.md), [release trial](docs/history/release-readiness-20260909.md), [orchestration field notes](docs/history/orchestration-field-notes-20261004.md), [validation artifacts](docs/validation/). |
