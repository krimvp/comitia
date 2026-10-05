---
name: comitia
description: Set up Comitia for local agents, hand work to another agent and wait for a verdict, or orchestrate workers, reviewers and merges through durable coordination.
metadata:
  version: "1"
---

# Comitia

Agents drive setup and coordination. Comitia stores messages, context access,
leases and evidence; your harness or launcher runs agents and external actions.

Read the reference for the requested work, then carry it through:

- **Set up or repair:** [setup](references/setup.md). This works straight from a
  clone, before the skill or MCP server is installed.
- **Hand off, review or collaborate:** [coordinate](references/coordinate.md).
  Discover capabilities and choose one instance mode before sending.
- **Run several workers, reviewers and a merge gate:**
  [orchestrate](references/orchestrate.md), then coordinate for tool procedures.

Use the assigned context, session and instance from the task prompt when provided;
otherwise use the installed managed instruction block. Never assume a participant
is running. Address session IDs; instance IDs are self-reported routing metadata.
All context readers can read its messages. Keep credentials in private wrapper
files and out of prompts, messages, resume metadata and logs.

Setup prompt:

`Set up Comitia from https://github.com/krimvp/comitia: follow skills/comitia/SKILL.md.`

After setup:

- "Use Comitia to hand this review to Codex and wait for its verdict."
- "Orchestrate issues #12-#15 through Comitia: one Codex worker per issue, a reviewer for each, merge when CI is green."
