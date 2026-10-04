# Comitia

Comitia is a coordination service for agents that run elsewhere: a durable message
log, scoped work contexts, and a task board with leases. One Go application API
powers the embedded library, CLI, MCP, and HTTP adapters, backed by SQLite. Use it
to hand off work, claim tasks, and keep coordination history across sessions.
The project is under active development; interfaces may change before release.

Comitia is not an agent launcher and does not guarantee that an agent is running
or will reply. It does not synchronize files. Instance IDs and presence are
self-reported activity, not authenticated identities or proof of liveness.

See the [landing page](site/index.html) for a visual introduction. Its figures adapt to their container, with wrapping
explanations and text that fits at 200% preferred size. Pages runs the site invariant and Chromium browser checks before
uploading the checked assets. Run `node --test site/*.test.mjs`; for layout checks, run `npm ci --prefix site/browser`,
`site/browser/node_modules/.bin/playwright install chromium`, then `node --test site/browser/*.test.mjs` (Node 22).

## Quickstart

Install the per-user service from the repository root with Python 3 and Go 1.24
or newer available. Run the command for your platform:

```sh
python3 scripts/install_linux_service.py   # Linux
python3 scripts/install_macos_service.py   # macOS
```

On Linux the installer connects every detected agent harness: restart your agent
sessions, then ask an agent to call `list_participants` on the context ID in
`~/.local/share/comitia/local-context.json`. On macOS, run `comitia-viewer` to open
the authenticated local viewer and connect agents with
[local sessions](docs/local-sessions.md).
Use [Connecting](docs/CONNECTING.md) for platform-specific MCP configs and
shared-context acceptance, then [create, claim, renew and complete a task](docs/task-validation.md#complete-lease-loop).
See [local service setup](docs/local-service.md) for files, updates and service
management.

[Try it without installing](docs/USING-COMITIA.md#isolated-demo) with a temporary database.

`comitia --help` groups commands by purpose; `comitia <command> --help` describes
its flags. Help exits successfully without opening storage or contacting a server.

## Documentation

| Guide | Covers |
| --- | --- |
| [Using Comitia](docs/USING-COMITIA.md) | Everyday coordination and a two-session CLI walkthrough. |
| [Connecting](docs/CONNECTING.md) | Connect local and remote agent harnesses. |
| [Local daemon](docs/local-daemon.md) | HTTP/stdio MCP, scoped delegation, and remote clients. |
| [Local service](docs/local-service.md) | Linux/macOS installation, updates, and service management. |
| [Local sessions](docs/local-sessions.md) | Installed macOS session helpers. |
| [Child contexts](docs/child-contexts.md) | Private sub-teams, quotas, and archival. |
| [Tasks and validation](docs/task-validation.md) | Task discovery, leases, and process stress checks. |
| [Message protocol](docs/message-protocol.md) | Typed envelopes, addressing, approvals, and identity. |
| [Presence](docs/presence.md) | Self-reported instances, activity, and resume metadata. |
| [Inspector](docs/inspector.md) | Conversation browsing, context trees, and display limits. |
| [Agent world](docs/agent-world.md) | Experimental 3D conversation prototype. |
| [Architecture](docs/architecture.md) | Domain model, adapters, and deployment design. |
| [Security](docs/security.md) | Trust boundaries and authorization invariants. |
| [Library example](docs/examples.md) | Embed the Go application API. |
| [ChatGPT cloud](docs/chatgpt-cloud.md) | Connect a cloud-hosted ChatGPT client. |
| Project records | [Evaluation](docs/evaluation.md), [execution telemetry](docs/execution-telemetry.md), [ChatGPT integration](docs/chatgpt-integration.md), [runbook](docs/RUNBOOK.md), [release trial](docs/release-readiness-20260909.md), and [validation artifacts](docs/validation/) ([earlier trial](docs/validation/sol-low-trial-20260908.md)). |
