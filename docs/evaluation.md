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
`scripts/evaluation_fixture_runner.py` (or the [Jev grader runner](#jev-grader-runner)
below), replace the fixture lookup with agent
execution plus an independent grader, and report every model call. Keep fixture
fields out of the real benchmark spec, and preserve grader evidence separately as
described below.

## Jev grader runner

`scripts/evaluation_jev_runner.py` is a runner adapter that replaces the fixture
lookup with an automated grader. It does not execute agents either: it reads the
artifact an agent produced for the trial from the spec, asks the Jev System One
API (`POST https://api.typesafe.ai/v1/systemone`, model `jev-latest`) one `noul`
question per declared quality check over the state `{"task": ..., "artifact": ...}`,
and prints the same JSON measurement as any other adapter. It uses only the
standard library, reads the key from `TYPESAFE_API_KEY`, and times out after 30 s.

The spec extension is per case:

| Field | Meaning |
| --- | --- |
| `task` | The case description sent as `state.task`. |
| `artifacts_by_mode` | Map from mode to the agent's output or transcript for that case: either the text itself or `{"path": "relative/file"}`, resolved relative to the spec file and confined to its directory. |
| `quality_checks[].instructions` | Optional statement Jev judges for that check. Without it the runner asks whether the artifact satisfies the check named in `name`. |

A check earns all of its points when its probability is strictly above 0.5 and
zero otherwise. The run metadata keeps the evidence: the probability of every
check, the model id Jev answered with, the artifact's SHA-256, and the seed. The
evaluator stores runner metadata verbatim in the result document, so a Jev result
is auditable per check without a separate evidence file.

Jev is an automated grader in the sense of `GraderKind` `automated` in
`internal/schema/evaluation.go`: a grader that is separate from the agents under evaluation and
whose evidence is calibrated probabilities, not a rerun of the agent. Reference
it that way in `comitia.evaluation-result.v1` messages that cite a Jev-graded
trial.

A missing key, a missing artifact, an HTTP error, a timeout, a non-JSON body, or
an answer that is not a `noul` probability is reported as `status: "failed"` with
every check at zero, so the evaluator scores the trial as a failed run and never
as a pass. `cost_complete` stays true when no request was sent and becomes false
after a request whose usage is unknown.

Usage is reported as one `model_calls` entry for `jev-latest`, so the spec must
price that model. `examples/evaluation/jev-benchmark.json` pins the provider's
published rate on 2026-09-17: USD 42 per billion input tokens (0.042 per million),
with output tokens not billed (source: <https://typesafe.ai/>). Re-pin it before a
trial; the rate is stated as subsidized and expected to move. The example has
two cases with hand-written artifacts per mode and a known expected pattern:
single-agent misses most checks, multi-agent misses one check per case, and
comitia meets every check. Run it with:

```sh
TYPESAFE_API_KEY=... python3 scripts/evaluate_agents.py run \
  --spec examples/evaluation/jev-benchmark.json \
  --runner ./scripts/evaluation_jev_runner.py \
  --output /tmp/comitia-jev-results.json \
  --markdown /tmp/comitia-jev-results.md
```

The adapter's tests fake the HTTP transport; `COMITIA_LIVE=1` with the key adds
one live run of the example:

```sh
python3 -m unittest scripts/test_evaluation_jev_runner.py
```

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
python3 -m unittest scripts/test_evaluate_agents.py scripts/test_evaluation_jev_runner.py
```
