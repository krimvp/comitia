# Orchestrating local agents: field notes and gaps

Archived 2026-10-05: a historical record of the 2026-10-04 orchestration run. Every gap below has since shipped;
the current orchestration recipe is the agent skill's
[orchestrate reference](../../skills/comitia/references/orchestrate.md). Kept for the evidence behind each feature.

| Gap | Issue | Shipped in |
| --- | --- | --- |
| 1-2. Waiting and filtered replay | [#13](https://github.com/krimvp/comitia/issues/13) | [#26](https://github.com/krimvp/comitia/pull/26), [#40](https://github.com/krimvp/comitia/pull/40) |
| 3. Presence and resume metadata | [#14](https://github.com/krimvp/comitia/issues/14) | [#30](https://github.com/krimvp/comitia/pull/30) |
| 4. Default instance from the environment | [#15](https://github.com/krimvp/comitia/issues/15) | [#27](https://github.com/krimvp/comitia/pull/27) |
| 5, 10. Field-level errors and `server_info` | [#16](https://github.com/krimvp/comitia/issues/16) | [#39](https://github.com/krimvp/comitia/pull/39) |
| 6-7. Typed approvals, instance-owned tasks | [#18](https://github.com/krimvp/comitia/issues/18) | [#24](https://github.com/krimvp/comitia/pull/24) |
| 8, 8a, 9, 11. Any harness, Linux installer | [#17](https://github.com/krimvp/comitia/issues/17) | [#21](https://github.com/krimvp/comitia/pull/21) |
| Delegated launch, task status | [#9](https://github.com/krimvp/comitia/issues/9), [#11](https://github.com/krimvp/comitia/issues/11) | [#36](https://github.com/krimvp/comitia/pull/36), [#25](https://github.com/krimvp/comitia/pull/25) |


On 2026-10-04 one Claude Code session ran a multi-issue project (krimvp/xpl issue #7 and its chain) on a single
Linux (WSL2) machine. It used Comitia as the coordination channel for up to six concurrent processes: Codex
implementation workers, Codex reviewers, a Codex merge agent, and a second Claude session the user started
independently. This document records what that took beyond what Comitia's docs describe, and what Comitia (or
its local installer) could do so the next orchestrator does not have to rediscover it.

Each gap gives the evidence from that run, then a proposal. Gaps already tracked by an issue link to it.

**Design goal: any harness.** The run used Claude Code and Codex, but an orchestrator or worker could be any
harness that speaks MCP or can run a command (Pi, Factory Droid, Cursor, a script). So the proposals below put
behaviour in three harness-neutral places: MCP tools every client sees, environment variables any launcher can
set, and a CLI that prints one line per event, which any harness can run or watch. What stays per-harness is
configuration only (how to auto-approve an MCP server, how to give a sandbox git access, where the instruction
file lives), and that belongs in a short per-harness table, not in Comitia's protocol or in agent prompts.

## The setup that worked

For reference, this is what a working orchestration looked like at the end of the run.

- One git worktree per process (`xpl-wt/issue-10`, `xpl-wt/review-10`, `xpl-wt/prep-13`, ...), one scratch
  folder per process outside `/tmp`, and one shared tool folder for toolchains.
- Codex launched as
  `cd <worktree> && codex exec -m gpt-6.1-sol -c model_reasoning_effort=<level> -s workspace-write
  -c sandbox_workspace_write.network_access=true -c 'mcp_servers.comitia.default_tools_approval_mode="approve"'
  --add-dir <repo>/.git -o <result file> - < <prompt file> > <log file>`.
- Every process had a self-reported instance ID (`claude-xpl-orch`, `codex-xpl-issue-10`, `codex-xpl-review-10-r1`,
  `codex-xpl-merger`, ...), a body tag (`[issue-10]`), and instance-prefixed dedupe keys.
- Lifecycle messages by convention: `started`, `progress`, `question`, `done`/`failed`; reviewers sent
  `round N: approve|changes-requested, k blocking`; the orchestrator certified merges with
  `[merge] ready: PR #N at <sha>`, which the merge agent acted on.
- The orchestrator learned that a message had arrived by tailing each Codex log for
  `mcp: comitia/send_message (completed)`, then calling `replay_messages`.

Almost every item in that list was discovered by failing first.

## Gaps in Comitia

### 1. No way to wait for a message ([#13](https://github.com/krimvp/comitia/issues/13))

**Evidence.** Comitia is poll-only. An orchestrating Claude session cannot sleep in a loop, so it tailed each
worker's log file and grepped for Comitia send events, then replayed. That couples the orchestrator to one
harness's log format (Codex), misses senders it did not launch (the second Claude session's messages arrived
unseen until the next replay), and breaks when the log line changes.

**Proposal.** A blocking read with a timeout: `wait_messages(context_id, after, timeout_seconds, recipient filter)`
over MCP and HTTP, returning as soon as a matching message is appended (or empty on timeout). Plus a CLI
`comitia-client <agent> watch --context-id ... --agent-instance ...` that prints one line per new message
addressed to that instance. A line-per-event CLI is exactly what harness monitors (Claude Code's Monitor tool,
`tail`-style watchers) consume, so the orchestrator would watch Comitia itself instead of agents' logs.

### 2. Replay cannot filter by recipient ([#13](https://github.com/krimvp/comitia/issues/13))

**Evidence.** Every reader replays the whole shared log and filters locally by `recipient_session_ids`,
`recipient_instances` and body tags. With six processes in one context, every worker paid for every other
worker's traffic, and each prompt had to explain the filtering rules.

**Proposal.** Optional replay filters: `for_instance` (messages addressed to that instance, or broadcasts with no
recipients), `from_instance`, `task_id`, and `schema`. Filters are a convenience over the same read grant; they
do not change visibility.

### 3. No liveness: is that agent still running? ([#14](https://github.com/krimvp/comitia/issues/14))

**Evidence.** Comitia correctly says it does not launch agents or guarantee they are running. But the
orchestrator needed to know exactly that. Two machine restarts and one Claude restart killed workers silently.
The orchestrator found out by checking `pgrep`, then recovered each worker by grepping
`~/.codex/sessions/**/rollout-*.jsonl` for the worktree path to find the Codex session ID to resume.
`list_participants` lists credentialed sessions, which are alive for a year; it says nothing about processes.

**Proposal.** Per-instance presence: the server records `last_seen` for each (session, instance) on any call that
carries an instance, and `list_instances(context_id)` returns instance ID, label, last seen, and optional
self-reported `resume` metadata (`{"harness": "codex", "session_id": "...", "cwd": "...", "pid": ...}`) that the
process can set once at start. An orchestrator can then see which workers went quiet and how to resume them,
without harness-specific file archaeology. Presence is self-reported, like instances, and never proof.

### 4. Every process must be told its instance ID in prose ([#15](https://github.com/krimvp/comitia/issues/15))

**Evidence.** Instances (#3/#4) fixed shared cursors, but each prompt had to repeat: pass `agent_instance` on every
send, the same ID string on cursor and ack, prefix every dedupe key, and fall back if the field is rejected. One
forgotten argument silently moves to the legacy shared cursor. Dedupe keys are scoped per credential session, so
two processes using the same natural key (`started`) collide unless they prefix it.

**Proposal.** `comitia-client <agent> mcp` (and the HTTP client) read `COMITIA_AGENT_INSTANCE` and
`COMITIA_AGENT_INSTANCE_LABEL` from the environment and apply them as the default instance on every call that
accepts one, including dedupe scoping. An explicit argument still wins. Launchers then set one environment
variable (`COMITIA_AGENT_INSTANCE=codex-xpl-issue-10 codex exec ...`) and prompts need no Comitia plumbing at all.
This complements #9 (per-agent delegated sessions): instance by default for loose collaboration, delegated
session when the agent owns a context.

### 5. Upgrades break running clients, and the error does not say why ([#16](https://github.com/krimvp/comitia/issues/16))

**Evidence.** Comitia was upgraded mid-run to add instances. The installer rewrote the global agent instructions
(`~/.codex/AGENTS.md`, `~/.claude/CLAUDE.md`) at once, so running agents read "pass `agent_instance`" and did, but
their MCP client processes were still the old binary and rejected the field. The only error was
`invalid input`. The orchestrator diagnosed it by reading the Codex session JSONL. Claude Code additionally kept
showing the old tool schema after a resume, although calls with the new field worked.

**Proposal.**
- Name the problem in errors: `invalid input: unknown field "agent_instance" (client comitia 0.x; server supports it
  from 0.y; restart the agent to load the new client)`. Today `ErrInvalid` is one sentinel for every validation
  failure.
- A `server_info` tool (version, feature list such as `agent_instances`) so instructions can say "if
  `server_info` lists `agent_instances`, ...", instead of assuming every running client was restarted.
- The installer prints which running agent processes still use the old client binary (it can find
  `comitia mcp` processes and compare their executable's inode with the new one).

### 6. Lifecycle conventions are free text ([#18](https://github.com/krimvp/comitia/issues/18))

**Evidence.** All coordination ran on body prefixes: `[issue-10] done: ...`, `[review-10] round 1: approve`,
`[merge] ready: PR #41 at <sha>`. The merge agent's whole safety gate rested on parsing a body prefix from the
right instance. The built-in schemas (`work-submitted`, `verification-result`, `review-request`) fit these
handoffs but require a `task_id`, and tasks were not used (gap 7), so nobody used the schemas.

**Proposal.**
- Allow `work-submitted` and `verification-result` without `task_id` when `artifact_refs` names the artifact
  (for example `git:<sha>` or `pr:krimvp/xpl#41@<sha>`), or make task creation cheap enough that it is always used.
- An approval schema, `comitia.approval.v1`: `artifact_refs` (exact commit), `verdict`
  (`approved | changes-requested`), `checks`, `blocking_count`. A merge agent then keys on a validated envelope
  from a given instance, not on prose. #11 (`task-status.v1`) covers progress reporting.

### 7. Tasks and leases went unused ([#18](https://github.com/krimvp/comitia/issues/18))

**Evidence.** The orchestrator created a Comitia task per issue, but no worker claimed one. With a shared
credential, a claim does not say which process owns the lease, and keeping a lease alive needs `renew_task` calls
that a long-running Codex worker will not reliably make between tool calls. So ownership lived in prompts and in
git branches instead.

**Proposal.** Record the claiming instance on a claim (`claim_task(..., agent_instance)`), show it in
`list_tasks`, and allow long or open-ended leases for tasks whose owner is a supervised process (released
explicitly by the orchestrator). With presence (gap 3), a task whose owner went silent is visible without lease
expiry.

## Gaps in the local installer and harness integration

These are not Comitia server features, but every local user hits them.

### 8. Headless agents cannot call Comitia by default ([#17](https://github.com/krimvp/comitia/issues/17))

Every harness that runs agents unattended has to auto-approve Comitia's MCP tools, and each does it differently.
Codex: `codex exec` runs with approval policy `never`, so every call failed with
`MCP tool call requires approval, but approval policy is never` until `default_tools_approval_mode = "approve"`
was set under `[mcp_servers.comitia]` in `~/.codex/config.toml`. Claude Code: an allow rule `mcp__comitia__*`.
Other harnesses (Pi, Factory Droid, ...) have their own switch. The installer registers the MCP server per
harness already; it should also set that harness's auto-approval for Comitia's tools (they only reach Comitia),
and the docs should keep a per-harness table: config file, MCP registration, auto-approval key, instruction file.

### 8a. The local wrapper only knows two harnesses ([#17](https://github.com/krimvp/comitia/issues/17))

`comitia-client` accepts exactly `codex` or `claude` as its identity argument and the installer writes managed
instruction blocks only to `~/.codex/AGENTS.md` and `~/.claude/CLAUDE.md`. Adding a third harness means editing
the wrapper and hand-copying the block. Make harnesses data: `comitia-client --identity <name>` (or the
`--credential-file` of #9) for any credential under `credentials/`, an installer command
`add-harness <name> --instructions <path> --mcp-config <path>`, and one instruction template rendered per harness.

### 9. The Linux install is not in the repository ([#17](https://github.com/krimvp/comitia/issues/17))

The Linux service (`systemctl --user`, `~/.local/share/comitia/`, the Python `comitia-client` wrapper,
`INSTALLATION.txt`, the managed blocks in `~/.claude/CLAUDE.md` and `~/.codex/AGENTS.md`) works, but its source is
not in the repo: `scripts/install_macos_service.py` and `docs/local-service.md` are macOS-only. Updates to the
wrapper or the managed instruction blocks cannot be reviewed or reproduced. Add the Linux installer and the
instruction-block templates to the repo, with `local-service.md` covering both systems.

### 10. Global instructions change under running agents ([#16](https://github.com/krimvp/comitia/issues/16))

The managed instruction blocks are global and are rewritten on install. Running agents pick up the new text on
their next instruction refresh, while their MCP client stays old (gap 5). Version the managed block
(`<!-- comitia-local:start v3 -->`) and have it tell agents what to do if a call rejects a field it describes.

### 11. Harness permissions for an orchestrator ([#17](https://github.com/krimvp/comitia/issues/17))

Whatever the harness, an orchestrator needs permission to start other agents, to read and write Comitia, and,
if it automates merges, to act on a repository without a human in the loop. In this run, Claude Code's auto mode blocked, in turn: launching Codex workers (until an allow rule `Bash(codex:*)`), editing
settings (correctly: an agent cannot grant itself permissions), writing a merge agent prompt, and then even plain
Comitia reads, once the conversation involved automated merging. It cleared after the user added
`mcp__comitia__*` to the allow list and an `autoMode.environment` note stating the authorized merge gate.
`docs/local-service.md` should document the allow rules a coordinating Claude session needs, and the installer
could offer to add `mcp__comitia__*` (Comitia's tools only reach Comitia).

## Not Comitia's job, but worth a recipe

These belong in an orchestration guide rather than in Comitia ([Orchestrating agents](../../skills/comitia/references/orchestrate.md)
is that guide, written for any harness). They were each found by failing; the examples are Codex-specific, but
every sandboxed harness raises the same questions:

- Codex's sandbox: a git worktree's metadata lives in the main repository's `.git`, so a worker needs
  `--add-dir <repo>/.git` to commit; even then commits often failed with "read-only file system" on
  `.git/worktrees/<name>/index.lock`, so the reliable pattern was: the worker prepares commit messages and file
  lists, the orchestrator commits and pushes.
- `codex exec resume` takes no `-s`/`--add-dir` (pass sandbox settings with `-c`) and uses the shell's current
  directory as its workdir, which it makes writable: always `cd <worktree>` first.
- Background processes started by a Claude Code session die when that session exits. Machine restarts wipe
  `/tmp` (prompts, logs, scratch). Keep orchestration files in a durable folder.
- GitHub does not let an account approve its own pull request, so when every agent pushes as the same user, the
  approval record has to live elsewhere (here: Comitia, see gap 6).
- A process-wide limit (five concurrent agents here) and a per-task reasoning effort chosen by the orchestrator
  kept cost and contention reasonable.

## Suggested order

1. Gaps 8 and 5 (auto-approval per harness, error detail, `server_info`): small, and they break every new user.
2. Gap 4 (default instance from the environment): small, removes most prompt plumbing and a class of mistakes.
3. Gaps 1 and 2 (wait and filtered replay): the largest improvement for orchestrators.
4. Gap 3 (presence and resume metadata), then 6 and 7 (typed handoffs, instance-owned tasks), alongside #9 and #11.
5. Gaps 8a to 11 (harnesses as data, installer in the repo, versioned instruction blocks, documented permissions).
