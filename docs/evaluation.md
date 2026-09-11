# Cost and quality evaluation

`scripts/evaluate_agents.py` compares single-agent, multi-agent, and
Comitia-coordinated execution over the same cases and repetitions. Comitia does
not launch models, so a provider-specific runner adapter performs each trial and
reports usage and independently measured quality back to the evaluator.

The evaluator provides the parts that must stay consistent across modes:

- a complete, paired matrix of case, repetition, and mode;
- a deterministic seed for each trial, derived from the benchmark seed and trial identity;
- fixed quality checks and point limits declared before execution;
- token costs derived from a versioned pricing snapshot, including cached input;
- failed-run quality of zero and explicit coverage for incomplete cost capture;
- per-mode quality, pass rate, cost, tokens, duration, and quality per dollar;
- paired quality and cost differences from the single-agent baseline, with
  deterministic bootstrap confidence intervals.

Model APIs can remain nondeterministic even when given a seed. Repetitions capture
some of that variation; record model snapshots and decoding settings in the spec's
free-form `metadata` object.

## Credential-free smoke test

The repository includes a concrete two-case fixture spec and a minimal runner
adapter. It exercises argument passing, the balanced trial matrix, quality and
cost validation, aggregation, and both JSON and Markdown output without calling
a model or requiring credentials:

```sh
python3 scripts/evaluate_agents.py run \
  --spec examples/evaluation/fixture-benchmark.json \
  --runner ./scripts/evaluation_fixture_runner.py \
  --output /tmp/comitia-fixture-results.json \
  --markdown /tmp/comitia-fixture-results.md

cat /tmp/comitia-fixture-results.md
```

The adapter reads the `fixture_quality_by_mode` scores and synthetic token usage
from the spec, then prints the same JSON measurement a provider adapter would
print. Those inputs are deterministic, although the evaluator records actual
subprocess wall time. The resulting comparison is only a harness smoke test; its
scores and prices are fixtures and are not evidence about agent performance.

To build a real adapter, copy the argument parsing and output shape from
`scripts/evaluation_fixture_runner.py`, replace the fixture lookup with agent
execution plus an independent grader, and report every model call. Keep fixture
fields out of the real benchmark spec, and preserve grader evidence separately as
described below.

## Benchmark specification

Create a JSON file like this. Pricing is USD per million tokens and must be pinned
to the date and provider terms used by the trial.

```json
{
  "schema": "comitia.eval-spec.v1",
  "seed": 20260911,
  "repetitions": 5,
  "bootstrap_samples": 5000,
  "runner_timeout_seconds": 1800,
  "modes": ["single-agent", "multi-agent", "comitia"],
  "cases": [
    {
      "id": "stale-lease-fix",
      "weight": 1,
      "pass_score": 8,
      "quality_checks": [
        {"name": "required-tests", "points": 6},
        {"name": "regression-review", "points": 3},
        {"name": "scope-discipline", "points": 1}
      ]
    }
  ],
  "pricing_usd_per_million_tokens": {
    "provider/model-snapshot": {
      "input": 2.0,
      "cached_input": 0.5,
      "output": 8.0
    }
  },
  "metadata": {
    "pricing_as_of": "2026-09-11",
    "repository_commit": "FULL_COMMIT_SHA",
    "temperature": 0
  }
}
```

Use several representative cases and at least five repetitions for a useful
comparison. Keep prompts, starting repository state, time limits, tool access,
model snapshots, and acceptance checks identical across modes. The only intended
difference should be the coordination strategy:

| Mode | Execution contract |
| --- | --- |
| `single-agent` | One model session owns implementation and verification. |
| `multi-agent` | The same agent pool may delegate, using the comparison coordination mechanism. |
| `comitia` | The same pool delegates and coordinates through Comitia. |

Count the orchestrator, implementers, reviewers, retries, summarizers, and failed
calls in every mode. Infrastructure shared by all modes may be omitted only when
the omission is documented and applied consistently. Put paid non-model services
in `non_model_cost_usd`.

## Runner adapter

For each trial, the evaluator invokes the executable supplied with `--runner`:

```text
RUNNER [--runner-arg values...] \
  --eval-spec ABSOLUTE_SPEC_PATH \
  --eval-mode MODE \
  --eval-case CASE_ID \
  --eval-replicate ZERO_BASED_INDEX \
  --eval-seed INTEGER
```

Run trials in isolated worktrees or containers reset to the commit in the spec.
The adapter should apply the case prompt, wait for execution, run deterministic
tests and/or a blinded rubric, and print exactly one JSON object to stdout:

```json
{
  "status": "completed",
  "quality": [
    {"name": "required-tests", "earned": 6},
    {"name": "regression-review", "earned": 2},
    {"name": "scope-discipline", "earned": 1}
  ],
  "model_calls": [
    {
      "model": "provider/model-snapshot",
      "input_tokens": 24000,
      "cached_input_tokens": 8000,
      "output_tokens": 3000
    }
  ],
  "non_model_cost_usd": 0,
  "cost_complete": true,
  "metadata": {"artifact": "worktree-or-result-reference"}
}
```

`input_tokens` includes cached input; the evaluator subtracts cached tokens before
applying the normal input rate. Quality check names must exactly match the case.
For `failed`, `timeout`, or `harness_error`, every quality score must be zero.

Set `cost_complete` to false if any call usage is unavailable. A timeout or
malformed runner response is automatically recorded with zero quality and
incomplete cost. Such runs remain in quality statistics, while cost coverage falls
and cost-efficiency metrics are suppressed. This prevents missing telemetry from
appearing as a zero-cost success or failure.

The runner's score must come from recorded test output or a grader that is separate
from the agents being evaluated. An agent's assertion that its work passed is not
quality evidence. Keep raw grader evidence and artifacts outside the summary and
reference them from runner metadata.

A runner may also publish task-linked
[`comitia.execution-event.v1`](execution-telemetry.md) messages while a trial is
running. A typical stream contains one `run_started` event, a `model_usage` event
for each model call, optional `tool_usage` events, and one `run_finished` event.
This durable telemetry complements the evaluator's result file; it does not
replace the runner's required stdout object, and `evaluate_agents.py` does not
ingest the events automatically.

## Run and summarize

Execute the matrix sequentially:

```sh
python3 scripts/evaluate_agents.py run \
  --spec benchmark.json \
  --runner ./runner-adapter \
  --output results.json \
  --markdown results.md
```

Pass fixed adapter options with repeated `--runner-arg`. Commands are executed
directly, without a shell. Sequential execution reduces cross-trial interference;
the adapter remains responsible for clean isolation.

To combine raw observation files produced elsewhere, each containing one run, an
array of runs, or a prior result document:

```sh
python3 scripts/evaluate_agents.py summarize \
  --spec benchmark.json \
  --observations trial-results/*.json \
  --output results.json \
  --markdown results.md
```

Summarization rejects missing or duplicate cells, unknown models, changed seeds,
undeclared checks, scores above their limits, cached-token counts above total
input, and prior result documents produced from different spec bytes. The result
records the SHA-256 of the exact spec bytes. Commit the spec,
grader, adapter, raw observations, and generated report when preserving a trial.

Run the evaluator tests with:

```sh
python3 -m unittest scripts/test_evaluate_agents.py
```
