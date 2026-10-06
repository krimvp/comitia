# Orchestrating agents through Comitia

One orchestrator runs workers, reviewers and a merge gate on one machine through
an epic context. The launcher runs processes; Comitia records coordination.
Read [coordinate](coordinate.md) before using tools. This recipe follows the
original orchestration field notes, updated for shipped features.

Contents: [roles](#roles), [identity](#identity-and-messages),
[layout](#workspace-layout), [loop](#the-loop), [merge](#certify-and-merge),
[recovery](#recovering), [harnesses](#per-harness-settings).

## Roles

| Role | Does | Never does |
| --- | --- | --- |
| Orchestrator | Splits work, launches/resumes processes, relays findings, issues certificates | Implements worker changes |
| Worker | Implements one task in its own worktree, reports progress, prepares commits | Touches another worker's files/branches |
| Reviewer | Reviews one exact commit, reruns checks, reports a verdict | Edits code |
| Merge agent | Merges certified green PRs under external policy | Edits code or merges uncertified heads |

Set a concurrency limit and choose reasoning effort for each task: mechanical
setup low, bounded work medium, cross-component work high, correctness-critical
work and reviews higher. Reviewers run at the worker's level or above.

## Identity and messages

Keep one stable instance per process, e.g. `codex-repo-issue-10` or
`claude-repo-orch`. Set `COMITIA_AGENT_INSTANCE` in each MCP launch environment
when supported; natural dedupe keys and independent cursors then work without
per-call plumbing. Otherwise follow coordinate's explicit/legacy branches; only
legacy mode needs instance-prefixed keys. Use both session and instance recipients.

Give each task owner a delegated session with minimal grants and a private
credential-file path; shared harness credentials plus instances suit loose
collaboration, not isolated authority. Discover participants and presence/resume
metadata, but do not equate either with running processes. Store harness run IDs,
worktrees and checkpoint state in the launcher's durable registry. After each
launch or resume, the launcher publishes nonsecret resume metadata itself:

```sh
comitia-client --credential-file PATH instance-resume --context-id RUN_CONTEXT \
  --agent-instance codex-repo-issue-10 --resume-file resume.json
```

`resume.json` is a JSON object (at most 4096 bytes: harness, run ID, cwd,
checkpoint), so workers need not call `set_instance_resume`.

Workers report with typed messages carrying their `task_id`, not free-text tags:

- `comitia.task-status.v1`: `in_progress` with the plan first and at milestones
  with numbers; `blocked` with `blockers` plus a routed question, continuing
  independent work; finally `review`, `done` or `failed` with `links` to results.
- `comitia.verification-result.v1` per check: `passed`/`failed` with
  `evidence_refs`, or `not_run` with `reason`.

Status never completes a task. Full reviews/logs go in scratch files; messages
carry the summary and path. Use bounded `wait_messages` when caught up, or a
`comitia watch` watcher instance (coordinate step 3) to be woken; process then ack
on the same consumer instance. Durable handling deduplicates committed effects
before acknowledging.
Use requests/receipts with absolute deadlines for handoffs and steering; a
watcher has its own cursor and does not consume the worker's business messages.

## Workspace layout

```text
<project>/            main checkout: nobody works here
<project>-wt/
  .orch/              durable prompts, logs, results and launcher registry
  .tools/<tool-ver>/  shared toolchains, never system-wide changes
  .scratch-<task>/    process scratch and handoff files
  <task>/             one worker worktree, one branch
  review-<task>/      detached reviewer worktree at the exact commit
```

## The loop

1. **Plan.** Read issues and dependencies; create tasks and explicit file ownership.
   Run independent groundwork early. Record trusted reviewer session assignments,
   policy revision, certificate issuer, merger and required checks outside messages.
   Open a run context: create a run task in the shared context, then
   `create_context(parent_context_id=SHARED, name, task_id)`. It needs `create`
   on the shared context (ask the installation owner; quota in coordinate). Create
   worker tasks and delegated sessions in the child; post the run task's
   `task-status.v1` with `child_context_id` in the shared context.
2. **Launch.** Delegate the owner's session; pass only its private credential-file
   path to the launcher. Persist launch intent, then actual process identity and
   harness run ID. Prompt with acceptance criteria, context/session/instance/task,
   worktree/scratch paths, repository rules, done checks and handoff requirements.
   Claim with a bounded lease; preserve token/version privately and renew on time.
3. **Steer.** Answer questions and scope decisions at design time. For durable
   controls, send `request.v1` with deadline/expected responder; wait for semantic
   receipts and checkpoint evidence. A missed deadline remains unresolved.
4. **Hand off.** Worker commits if allowed. When git metadata is read-only, it
   writes the commit message and file list to scratch. Orchestrator reviews the
   diff, commits, reruns fast checks, pushes and opens the PR within user authority.
5. **Review.** Launch a reviewer on the exact head in a fresh detached worktree.
   Use `comitia.request.v1` operation `review`, future deadline, assigned reviewer
   session IDs and exact `pr:owner/repo#N@FULL_SHA` artifact refs. Reviewer reruns
   every check and sends `comitia.approval.v1` replying to that request with exact
   artifact refs, `verdict`, `checks` and `blocking_count`. Each finding is a
   `comitia.finding.v1` with file, line and reproduction (`priority`, `size`);
   panelists answer it with `comitia.stance.v1` replies, not votes or duplicate
   findings. Tally by replaying with `reply_to`.
6. **Fix rounds.** Confirm findings before relaying. Resume the same worker with
   confirmed changes; fixes need meaningful regressions. Every new head requires
   a new review request, approvals and certificate. Complete the task separately
   with the latest claim token/version after its acceptance criteria are satisfied.
7. **Certify and merge.** Follow the gate below; a prose “ready” message alone
   cannot authorize merging. Retarget stacked PRs within the approved workflow.
8. **Close.** Reconcile effects, complete tasks, revoke delegated sessions, post
   the final run status, then `archive_context` the run context (irreversible).

## Certify and merge

Issue `comitia.merge-certificate.v1` replying to the review request, matching its
task if present. Payload: trusted `policy_id`, exact `artifact_ref` and `head_sha`,
`approval_ids`, RFC3339 `issued_at`/`expires_at`, unique `consumption_key`.
Use full lowercase 40/64-character SHAs. Server validation checks correlation;
it does not grant merge authority.

The external gate validates authenticated issuer/requester/reviewer session IDs,
expected responders, all approvals, required checks, exact current artifact and
freshness. Go's `VerifyMergeCertificate` verifies that snapshot only. Independently
read through a chosen high-water mark for later changes-requested verdicts,
replacement requests, policy changes, cancellations or changed task attempts.
Require current head, green CI, correct base and host mergeability; recheck head
immediately before mutation and use an expected-head precondition.

Durably reserve the consumption key under an external lock/unique registry before
GitHub mutation. If the response is lost, reconcile the PR, expected head and
resulting merge commit; closure alone does not prove this operation merged it.
Only after reconciliation publish `comitia.merge-receipt.v1` replying to the
certificate with matching `operation_id`, head, status, exact evidence refs and
`merge_sha` when merged. Retry receipt publication with the same dedupe key,
never repeat the merge just because a receipt failed. `not_merged` is terminal
for that operation; another attempt needs policy authorization and a new key.
See [external gates](https://github.com/krimvp/comitia/blob/main/docs/durable-coordination.md#exact-sha-certificates-and-merge-reconciliation)
for the complete reservation and invalidation contract.

## Recovering

- Reconcile the launcher's durable registry against host/boot/process-start
  identity, actual exits and harness state. A matching PID or recent presence is
  insufficient. Worktrees and uncommitted changes survive a launcher restart.
- Resume the recorded harness run in its worktree with missed decisions and
  outstanding effects, then rerun `comitia instance-resume`. Use `list_instances`
  resume metadata as a hint, not trusted launch identity. Restarted sessions with
  new credentials need new routing IDs.
- Before reclaiming an expired lease or launching a replacement, reconcile effects
  and stop/confirm the old process through the launcher; fence future task results.
  Lease expiry does not stop processes. Never reclaim solely on stale last_seen.
- After upgrades, running clients retain old schemas/binaries. Follow coordinate's
  compatibility branch and restart them; do not silently drop required filters.

## Per-harness settings

Verify options with the installed harness's help. Other harnesses use their own
non-interactive mode, sandbox, approval configuration and session registry.

| Setting | Codex CLI 0.160 | Claude Code | Other harnesses |
| --- | --- | --- | --- |
| Headless launch | `codex exec -m MODEL -c model_reasoning_effort=LEVEL - < prompt` | `claude -p` | documented headless mode |
| Per-launch instance | `COMITIA_AGENT_INSTANCE=codex-worker COMITIA_AGENT_INSTANCE_LABEL=Worker codex exec ...` (installer forwards both via `env_vars`); override with `-c 'mcp_servers.comitia.env.COMITIA_AGENT_INSTANCE="codex-worker"'` | `COMITIA_AGENT_INSTANCE=claude-worker COMITIA_AGENT_INSTANCE_LABEL=Worker claude -p ...` | forward both variables to MCP |
| Comitia approval | `-c 'mcp_servers.comitia.default_tools_approval_mode="approve"'` | allow `mcp__comitia__*` | per-server/tool approval |
| Worktree/network sandbox | `-s workspace-write -c sandbox_workspace_write.network_access=true` | permission mode/allow rules | inspect policy |
| Worktree git metadata | `--add-dir REPO/.git`; if read-only use scratch handoff | normally allowed; inspect policy | metadata lives in REPO/.git/worktrees |
| Resume | `cd WORKTREE && codex exec resume RUN_ID` (inspect resume help; launch sandbox flags differ) | `claude --resume RUN_ID` | documented resume command |
| Run ID | launcher output/registry; do not assume rollout file layout | launcher output/registry | record at launch |
| Global instructions | `~/.codex/AGENTS.md` | `~/.claude/CLAUDE.md` | documented instruction file |
| User skills | `~/.agents/skills` | `~/.claude/skills` | verified loader or fallback |
| Launch other agents | sandbox/approval must permit command | allow launcher commands, e.g. `Bash(codex:*)` | documented command allow-list |

Set instance env on each Comitia MCP process; do not reuse a common fixed instance
for all workers. Delegated MCP launch overrides are in
[local service](https://github.com/krimvp/comitia/blob/main/docs/local-service.md#spawn-flow-a-delegated-session-per-task-owner).
