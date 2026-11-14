# Documentation

One line per document: who it is for and what it answers. The groups follow the
[README](../README.md#documentation).

## Use

- [Agent skill](../skills/comitia/SKILL.md): for agents; which reference to follow for setup, handoff or orchestration.
- [Skill: setup](../skills/comitia/references/setup.md): for agents setting Comitia up for a user; what to install, connect and verify.
- [Skill: coordinate](../skills/comitia/references/coordinate.md): for agents; how to send, wait, handle and acknowledge, and hand work to another agent.
- [Skill: orchestrate](../skills/comitia/references/orchestrate.md): for orchestrating agents; how to run workers, reviewers and a merge gate through one context.
- [Using Comitia](USING-COMITIA.md): for people driving Comitia by hand; the first handoff, the commands and an isolated demo.

## Run

- [Local service](local-service.md): for operators; how to install, repair and remove the per-user service on Linux and macOS.
- [Connecting harnesses](CONNECTING.md): for operators; how to give each harness a scoped credential on a shared context.
- [Local sessions](local-sessions.md): for agents on the macOS service; how `comitia-local` scopes identities.
- [Local daemon and remote client](local-daemon.md): for integrators; the daemon transports and the REST wire contract.
- [Inspector](inspector.md): for people watching coordination; what the read-only inspector shows.
- [Backup and restore](backup-restore.md): for operators; how to take a snapshot and prove a restore works.

## Reference

- [Tasks](task-validation.md): task discovery, leases, terminal outcomes and their concurrency rules.
- [Messages](message-protocol.md): envelopes and schemas, replay and waiting, identity, errors and compatibility.
- [Durable coordination](durable-coordination.md): durable consumers, requests and receipts, merge gates and supervised processes.
- [Child contexts](child-contexts.md): private sub-team contexts and what their parents can see.
- [Presence](presence.md): self-reported instance activity and resume metadata.
- [Architecture](architecture.md): the model, the package boundaries and the consistency rules.
- [Security](security.md): the threat model and authorization invariants.
- [Library example](examples.md): for Go programs embedding Comitia; a compiled embedded program.
- [Evaluation](evaluation.md): how to compare single-agent, multi-agent and Comitia runs on cost and quality.
- [Execution telemetry](execution-telemetry.md): how runners record execution events.
- [Agent world](agent-world.md): the experimental 3D viewer.

## Contributing

- [Contributing](../CONTRIBUTING.md): for contributors, people or agents; prerequisites, build, tests, recipes and rules.
- [Code map](code-map.md): for contributors; where each domain lives and how a request travels through the code.
- [Scripts](../scripts/README.md): for contributors; what each script does and which tests cover it.

## History

Dated records of the past; their commands and paths may no longer work.

- [Deployment runbook](history/RUNBOOK.md): the 2026-09 single-host deployment.
- [Cloud connector](history/chatgpt-cloud.md) and [integration plan](history/chatgpt-integration.md): the 2026-10 ChatGPT work.
- [Release trial](history/release-readiness-20260909.md): the six-agent release-hardening trial.
- [Orchestration field notes](history/orchestration-field-notes-20261004.md): the gaps found in one orchestration run.
- [Local service validation](history/local-service-validation-20261005.md) and [inspector execution view](history/inspector-execution-view-20261005.md): archived observations and a design record.
- [Validation artifacts](validation/): raw trial results.
