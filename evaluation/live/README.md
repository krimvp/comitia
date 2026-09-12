# September 11, 2026 live evaluation

Repository: `570583b0d6cd2ce8ba8e05f1cce6d61b710db611` (latest remote main when cloned).

The older `/Users/taniaprado/workspace/comitia` directory had no commits or tracked implementation files and lacked the evaluation harness. It was left untouched. This checkout is isolated under the Comitia project mirror, outside its read-only `sources/` directory.

## What is measured

Six bounded, self-contained tasks cover factual answers, Python refactoring, Python debugging, outbox architecture, constrained delegation scheduling, and incident communication. Five repetitions per task and mode produce 90 trials. All use `claude-haiku-4-5-20251001` through Claude Code 2.1.263. This is an exploratory benchmark across categories, not a production task distribution or a comparison across frontier models.

- Single agent: one fresh model session returns the answer.
- Multi-agent baseline: a fresh worker session answers; a separate reviewer session receives the task and worker answer and produces the final answer.
- Comitia: same worker and reviewer prompts, with actual scoped session delegation, task creation and claims, worker send, reviewer replay and acknowledgement, two task completions, and final state verification. The adapter performs coordination on behalf of the model sessions. It strips transport metadata before giving the reviewer the exact answer body.

The two collaborative modes isolate the message transport under a fixed workflow. They do not test whether models independently choose good Comitia tools, whether dynamic delegation beats other policies, or whether extra agents help on large repository tasks. Models have no tools; the grader executes submitted Python separately. No agent edits the repository being evaluated. Both collaborative modes use two serial model sessions and are not parallel speedup experiments.

Each model-client call has a 100-second timeout; each complete trial has a 240-second harness timeout. A collaborative trial can make two calls. A client timeout may hide in-flight usage, so that trial is marked cost-incomplete rather than free.

Trial order rotates across modes by case and repetition to reduce time/order effects. Execution is sequential. Each session runs in a fresh temporary directory with hooks, skills, tools, MCP and prompt caching disabled. The evaluator records seeds, but the CLI exposes neither model sampling seed nor temperature for this run; repetitions are not guaranteed deterministic or independent samples. Inputs and grading rules were fixed before the matrix began.

## Quality and costs

Quality is the fraction of fixed checks passed within each task; all checks must pass for a task pass. Tasks have equal aggregate weight. Python checks execute candidate code and include edge cases, randomized behavior, immutability and a deterministic work-growth check. Architecture and communication scores check stated facts and constraints; they are not holistic expert quality scores. Read raw answers alongside those scores.

The grader is separate from evaluated model calls and never sees the mode. `test_grader.py` validates reference answers and deliberately broken variants. Grader evidence is preserved per trial. Post-run worker grading uses the identical rubric to identify reviewer improvements or regressions.

`model_calls` records the CLI's per-model aggregate `modelUsage`, including any auxiliary calls. Input totals include cache reads, output totals include reported thinking tokens. Every trial reconciles computed cost to CLI `total_cost_usd`; unknown model, unexpected cache-write usage, incomplete execution or a mismatch marks cost incomplete. Prices are the public standard Haiku 4.5 rates ($1 input, $0.10 cached input, $5 output per million tokens), accessed September 11, 2026: https://platform.claude.com/docs/en/about-claude/pricing. These are list-price equivalents, not subscription charges. Shared local computing and electricity costs are excluded; local coordination time is included in wall time.

One small model capability probe and one Comitia integration pilot are outside the matrix and preserved separately. Fixture scores are synthetic and excluded from all real-model conclusions. Infrastructure stress results exercise subprocess clients, not LLM agents. No real-trial failure is discarded or silently retried.

## Reproduce

From this repository root, with the `claude` CLI authenticated:

```sh
go build -o bin/comitia ./cmd/comitia
python3 -m unittest discover -s evaluation/live -p 'test_*.py'
python3 evaluation/live/run_matrix.py
python3 evaluation/live/analyze.py
python3 scripts/evaluate_agents.py summarize --spec evaluation/live/benchmark.json --observations evaluation/live/observations/*.json --output evaluation/live/revalidated.json --markdown evaluation/live/revalidated.md
```

`run_matrix.py` calls the repository's `invoke_runner`, validation, aggregation, cost and bootstrap functions. It adds counterbalanced ordering and immediate per-trial persistence; the original harness is unchanged. Existing observation files are resumed, so archive the `observations/` and `artifacts/` directories before starting a new experiment. The specification is in `benchmark.json`; `create_spec.py` preserves its construction.

`results.json` and `results.md` are the original harness aggregate formats. `per-case.md` and `analysis.json` add case-level and reviewer-change detail. `artifacts/` contains prompts, raw client responses with usage, final answers, grader checks and credential-free operation traces. Temporary Comitia databases and session credentials are removed after each trial.

The only change to the supplied implementation is the executable bit on `scripts/evaluation_fixture_runner.py`, required by its documented invocation and existing test. No changes are committed or pushed.

## Score audit

`audit_scores.py` preserves the original run observations and separately regrades saved answers. The `audited` series excludes the ambiguous scheduling handoff check uniformly (six scheduling checks remain), and corrects one scorer bug: architecture entity names are case-insensitive and equivalent OutboxMessage/OutboxEntry labels normalize to outbox, since the prompt did not specify capitalization. The `content-sensitivity` series additionally extracts the first JSON object from surrounding prose. This second analysis was added after observing formatting failures; it is explicitly post-hoc and does not replace the requirement-compliance score. Seven additional tests validate the score audit, including rejection of genuinely wrong facts/entities. All 17 grader/audit tests pass.

`manual-audit-notes.md` under the repository's `results/` directory records qualitative inspection. The schedule prompt's handoff example is ambiguous between literal field names and example values; strict handoff failures are labeled nonconformance rather than proof of unsafe execution. Automatic communication checks do not score every prose nuance.

There is no equal-token-budget single-agent self-review arm. This experiment can show what an additional reviewer changed, but cannot establish that a distinct agent identity is necessary for that improvement.

Audited reanalysis uses its own saved `audited-benchmark.json` specification and hash; the content sensitivity analysis uses `content-sensitivity-benchmark.json`. The original execution specification and all as-run scores are retained. These post-hoc corrections do not alter any model prompt or rerun a model.
