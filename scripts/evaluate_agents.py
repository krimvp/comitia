#!/usr/bin/env python3
"""Run and summarize reproducible single-agent, multi-agent, and Comitia trials.

The evaluator deliberately does not know how to launch an agent. A runner adapter
owns that integration and emits one JSON measurement per trial. This script owns
the balanced trial matrix, seeds, validation, price calculation, and aggregation.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import random
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path
from typing import Any


SPEC_SCHEMA = "comitia.eval-spec.v1"
RESULT_SCHEMA = "comitia.eval-results.v1"
MODES = ("single-agent", "multi-agent", "comitia")
MAX_OUTPUT_BYTES = 4 * 1024 * 1024


class EvaluationError(ValueError):
    pass


def _object(value: Any, label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise EvaluationError(f"{label} must be an object")
    return value


def _number(value: Any, label: str, *, integer: bool = False) -> float | int:
    valid = isinstance(value, int) and not isinstance(value, bool) if integer else isinstance(value, (int, float)) and not isinstance(value, bool)
    finite = valid and (not isinstance(value, float) or math.isfinite(value))
    if not valid or not finite or value < 0:
        kind = "non-negative integer" if integer else "non-negative number"
        raise EvaluationError(f"{label} must be a {kind}")
    return value


def load_spec(path: str | Path) -> tuple[dict[str, Any], bytes]:
    raw = Path(path).read_bytes()
    try:
        spec = _object(json.loads(raw), "spec")
    except json.JSONDecodeError as exc:
        raise EvaluationError(f"invalid spec JSON: {exc}") from exc
    if spec.get("schema") != SPEC_SCHEMA:
        raise EvaluationError(f"spec schema must be {SPEC_SCHEMA!r}")
    modes = spec.get("modes")
    if not isinstance(modes, list) or not all(isinstance(mode, str) for mode in modes) or len(modes) != len(set(modes)) or set(modes) != set(MODES):
        raise EvaluationError(f"modes must contain exactly {', '.join(MODES)}")
    repetitions = _number(spec.get("repetitions"), "repetitions", integer=True)
    if not 1 <= repetitions <= 1000:
        raise EvaluationError("repetitions must be in 1..1000")
    _number(spec.get("seed"), "seed", integer=True)
    timeout = _number(spec.get("runner_timeout_seconds", 1800), "runner_timeout_seconds")
    if timeout <= 0:
        raise EvaluationError("runner_timeout_seconds must be greater than zero")
    samples = _number(spec.get("bootstrap_samples", 2000), "bootstrap_samples", integer=True)
    if not 100 <= samples <= 100000:
        raise EvaluationError("bootstrap_samples must be in 100..100000")

    cases = spec.get("cases")
    if not isinstance(cases, list) or not cases:
        raise EvaluationError("cases must be a non-empty array")
    case_ids: set[str] = set()
    for index, case_value in enumerate(cases):
        case = _object(case_value, f"cases[{index}]")
        case_id = case.get("id")
        if not isinstance(case_id, str) or not case_id.strip() or case_id in case_ids:
            raise EvaluationError(f"cases[{index}].id must be a unique non-empty string")
        case_ids.add(case_id)
        weight = _number(case.get("weight", 1), f"case {case_id!r} weight")
        if weight <= 0:
            raise EvaluationError(f"case {case_id!r} weight must be greater than zero")
        checks = case.get("quality_checks")
        if not isinstance(checks, list) or not checks:
            raise EvaluationError(f"case {case_id!r} quality_checks must be a non-empty array")
        names: set[str] = set()
        total = 0.0
        for check_index, check_value in enumerate(checks):
            check = _object(check_value, f"case {case_id!r} quality_checks[{check_index}]")
            name = check.get("name")
            if not isinstance(name, str) or not name.strip() or name in names:
                raise EvaluationError(f"case {case_id!r} check names must be unique non-empty strings")
            names.add(name)
            points = _number(check.get("points"), f"case {case_id!r} check {name!r} points")
            if points <= 0:
                raise EvaluationError(f"case {case_id!r} check {name!r} points must be greater than zero")
            total += points
        pass_score = _number(case.get("pass_score", total), f"case {case_id!r} pass_score")
        if pass_score > total:
            raise EvaluationError(f"case {case_id!r} pass_score exceeds available points")

    pricing = _object(spec.get("pricing_usd_per_million_tokens"), "pricing_usd_per_million_tokens")
    if not pricing:
        raise EvaluationError("pricing_usd_per_million_tokens must not be empty")
    for model, rates_value in pricing.items():
        if not isinstance(model, str) or not model:
            raise EvaluationError("pricing model names must be non-empty strings")
        rates = _object(rates_value, f"pricing for {model!r}")
        if set(rates) != {"input", "cached_input", "output"}:
            raise EvaluationError(f"pricing for {model!r} must contain only input, cached_input, and output")
        for key, value in rates.items():
            _number(value, f"pricing for {model!r}.{key}")
    return spec, raw


def trial_seed(seed: int, mode: str, case_id: str, replicate: int) -> int:
    value = f"{seed}\0{mode}\0{case_id}\0{replicate}".encode()
    return int.from_bytes(hashlib.sha256(value).digest()[:8], "big") & 0x7FFFFFFF


def _failed_run(case: dict[str, Any], mode: str, replicate: int, seed: int, status: str, error: str, wall: float) -> dict[str, Any]:
    return {
        "mode": mode,
        "case_id": case["id"],
        "replicate": replicate,
        "seed": seed,
        "status": status,
        "wall_seconds": round(wall, 6),
        "quality": [{"name": check["name"], "earned": 0} for check in case["quality_checks"]],
        "model_calls": [],
        "non_model_cost_usd": 0,
        "cost_complete": False,
        "error": error[:2000],
    }


def invoke_runner(runner: str, runner_args: list[str], spec_path: Path, spec: dict[str, Any], case: dict[str, Any], mode: str, replicate: int) -> dict[str, Any]:
    seed = trial_seed(spec["seed"], mode, case["id"], replicate)
    command = [runner, *runner_args, "--eval-spec", str(spec_path), "--eval-mode", mode,
               "--eval-case", case["id"], "--eval-replicate", str(replicate), "--eval-seed", str(seed)]
    started = time.monotonic()
    try:
        # File-backed capture prevents a noisy runner from consuming unbounded
        # evaluator memory before the output-size check can run.
        with tempfile.TemporaryFile() as stdout, tempfile.TemporaryFile() as stderr:
            proc = subprocess.Popen(command, stdin=subprocess.DEVNULL, stdout=stdout, stderr=stderr,
                                    start_new_session=os.name == "posix")
            try:
                proc.wait(timeout=spec.get("runner_timeout_seconds", 1800))
            except subprocess.TimeoutExpired:
                if os.name == "posix":
                    os.killpg(proc.pid, signal.SIGKILL)
                else:
                    proc.kill()
                proc.wait()
                return _failed_run(case, mode, replicate, seed, "timeout", "runner timed out",
                                   time.monotonic() - started)
            stdout_size, stderr_size = stdout.tell(), stderr.tell()
            stdout.seek(0)
            stderr.seek(0)
            proc.stdout = stdout.read(MAX_OUTPUT_BYTES + 1)
            proc.stderr = stderr.read(MAX_OUTPUT_BYTES + 1)
    except OSError as exc:
        return _failed_run(case, mode, replicate, seed, "harness_error", str(exc), time.monotonic() - started)
    wall = time.monotonic() - started
    if stdout_size > MAX_OUTPUT_BYTES or stderr_size > MAX_OUTPUT_BYTES:
        stream = "stdout" if stdout_size > MAX_OUTPUT_BYTES else "stderr"
        return _failed_run(case, mode, replicate, seed, "harness_error", f"runner {stream} exceeded 4 MiB", wall)
    try:
        reported = _object(json.loads(proc.stdout), "runner output")
    except (json.JSONDecodeError, UnicodeDecodeError, EvaluationError) as exc:
        detail = proc.stderr.decode(errors="replace").strip() if proc.stderr else str(exc)
        return _failed_run(case, mode, replicate, seed, "harness_error", detail or str(exc), wall)
    if proc.returncode != 0 and reported.get("status") == "completed":
        detail = proc.stderr.decode(errors="replace").strip()[:2000]
        return _failed_run(case, mode, replicate, seed, "failed", detail or f"runner exited {proc.returncode}", wall)
    reported.update({"mode": mode, "case_id": case["id"], "replicate": replicate,
                     "seed": seed, "wall_seconds": round(wall, 6)})
    if proc.returncode != 0 and "error" not in reported:
        reported["error"] = proc.stderr.decode(errors="replace").strip()[:2000] or f"runner exited {proc.returncode}"
    try:
        return validate_run(reported, spec, expected_key=(mode, case["id"], replicate))
    except EvaluationError as exc:
        return _failed_run(case, mode, replicate, seed, "harness_error", str(exc), wall)


def validate_run(run_value: Any, spec: dict[str, Any], *, expected_key: tuple[str, str, int] | None = None) -> dict[str, Any]:
    run = _object(run_value, "run")
    mode, case_id, replicate = run.get("mode"), run.get("case_id"), run.get("replicate")
    if mode not in spec["modes"]:
        raise EvaluationError(f"run has unknown mode {mode!r}")
    cases = {case["id"]: case for case in spec["cases"]}
    if not isinstance(case_id, str) or case_id not in cases:
        raise EvaluationError(f"run has unknown case {case_id!r}")
    _number(replicate, "run replicate", integer=True)
    if replicate >= spec["repetitions"]:
        raise EvaluationError("run replicate is outside the spec")
    key = (mode, case_id, replicate)
    if expected_key is not None and key != expected_key:
        raise EvaluationError(f"runner changed trial identity: expected {expected_key}, got {key}")
    expected_seed = trial_seed(spec["seed"], mode, case_id, replicate)
    if run.get("seed") != expected_seed:
        raise EvaluationError(f"run {key} has an incorrect seed")
    status = run.get("status")
    if not isinstance(status, str) or status not in {"completed", "failed", "timeout", "harness_error"}:
        raise EvaluationError(f"run {key} has invalid status {status!r}")
    _number(run.get("wall_seconds"), f"run {key} wall_seconds")
    if not isinstance(run.get("cost_complete"), bool):
        raise EvaluationError(f"run {key} cost_complete must be boolean")
    _number(run.get("non_model_cost_usd", 0), f"run {key} non_model_cost_usd")

    expected_checks = {check["name"]: check["points"] for check in cases[case_id]["quality_checks"]}
    checks = run.get("quality")
    if not isinstance(checks, list):
        raise EvaluationError(f"run {key} quality must be an array")
    actual: dict[str, float] = {}
    for value in checks:
        check = _object(value, f"run {key} quality check")
        name = check.get("name")
        if not isinstance(name, str) or name not in expected_checks or name in actual:
            raise EvaluationError(f"run {key} has an unknown or duplicate quality check {name!r}")
        earned = _number(check.get("earned"), f"run {key} check {name!r} earned")
        if earned > expected_checks[name]:
            raise EvaluationError(f"run {key} check {name!r} exceeds its available points")
        actual[name] = earned
    if set(actual) != set(expected_checks):
        raise EvaluationError(f"run {key} must report every declared quality check")
    if status != "completed" and any(actual.values()):
        raise EvaluationError(f"non-completed run {key} must receive zero quality")

    calls = run.get("model_calls")
    if not isinstance(calls, list):
        raise EvaluationError(f"run {key} model_calls must be an array")
    pricing = spec["pricing_usd_per_million_tokens"]
    for index, call_value in enumerate(calls):
        call = _object(call_value, f"run {key} model_calls[{index}]")
        model = call.get("model")
        if not isinstance(model, str) or model not in pricing:
            raise EvaluationError(f"run {key} uses unpriced model {model!r}")
        for field in ("input_tokens", "cached_input_tokens", "output_tokens"):
            _number(call.get(field), f"run {key} call {index} {field}", integer=True)
        if call["cached_input_tokens"] > call["input_tokens"]:
            raise EvaluationError(f"run {key} call {index} cached input exceeds input tokens")
    return run


def cost_of(run: dict[str, Any], spec: dict[str, Any]) -> float:
    total = float(run.get("non_model_cost_usd", 0))
    for call in run["model_calls"]:
        rates = spec["pricing_usd_per_million_tokens"][call["model"]]
        uncached = call["input_tokens"] - call["cached_input_tokens"]
        total += (uncached * rates["input"] + call["cached_input_tokens"] * rates["cached_input"] + call["output_tokens"] * rates["output"]) / 1_000_000
    return total


def quality_of(run: dict[str, Any], case: dict[str, Any]) -> tuple[float, bool]:
    possible = sum(check["points"] for check in case["quality_checks"])
    earned = sum(check["earned"] for check in run["quality"])
    return earned / possible, run["status"] == "completed" and earned >= case.get("pass_score", possible)


def _mean(values: list[float]) -> float | None:
    return sum(values) / len(values) if values else None


def _weighted_mean(values: list[float], weights: list[float]) -> float:
    return sum(value * weight for value, weight in zip(values, weights)) / sum(weights)


def _bootstrap_ci(values: list[float], samples: int, seed: int, *, weights: list[float] | None = None,
                  digits: int = 6) -> list[float] | None:
    if not values:
        return None
    rng = random.Random(seed)
    weights = weights or [1.0] * len(values)
    if len(weights) != len(values):
        raise ValueError("bootstrap values and weights must have equal lengths")
    pairs = list(zip(values, weights))
    means = []
    for _ in range(samples):
        sample = [rng.choice(pairs) for _ in pairs]
        means.append(_weighted_mean([pair[0] for pair in sample], [pair[1] for pair in sample]))
    means.sort()
    low = means[int(0.025 * (samples - 1))]
    high = means[int(0.975 * (samples - 1))]
    return [round(low, digits), round(high, digits)]


def summarize(spec: dict[str, Any], runs: list[dict[str, Any]]) -> dict[str, Any]:
    by_key: dict[tuple[str, str, int], dict[str, Any]] = {}
    for run_value in runs:
        run = validate_run(run_value, spec)
        key = (run["mode"], run["case_id"], run["replicate"])
        if key in by_key:
            raise EvaluationError(f"duplicate run {key}")
        by_key[key] = run
    expected = {(mode, case["id"], replicate) for mode in spec["modes"]
                for case in spec["cases"] for replicate in range(spec["repetitions"])}
    missing = sorted(expected - set(by_key))
    extra = sorted(set(by_key) - expected)
    if missing or extra:
        raise EvaluationError(f"unbalanced trial matrix: missing={missing[:5]}, extra={extra[:5]}")

    cases = {case["id"]: case for case in spec["cases"]}
    mode_summary: dict[str, Any] = {}
    samples = spec.get("bootstrap_samples", 2000)
    for mode_index, mode in enumerate(spec["modes"]):
        selected = [by_key[key] for key in sorted(by_key) if key[0] == mode]
        quality_values = [quality_of(run, cases[run["case_id"]])[0] for run in selected]
        quality_weights = [cases[run["case_id"]].get("weight", 1) for run in selected]
        quality_mean = _weighted_mean(quality_values, quality_weights)
        complete_costs = [cost_of(run, spec) for run in selected if run["cost_complete"]]
        token_fields = {name: sum(call[name] for run in selected for call in run["model_calls"])
                        for name in ("input_tokens", "cached_input_tokens", "output_tokens")}
        passes = sum(quality_of(run, cases[run["case_id"]])[1] for run in selected)
        all_costs_known = len(complete_costs) == len(selected)
        cost_total = sum(complete_costs)
        mode_summary[mode] = {
            "runs": len(selected),
            "completed_runs": sum(run["status"] == "completed" for run in selected),
            "passing_runs": passes,
            "quality_mean": round(quality_mean, 6),
            "quality_mean_95ci": _bootstrap_ci(quality_values, samples, spec["seed"] + mode_index, weights=quality_weights),
            "known_cost_usd": round(cost_total, 9),
            "cost_coverage": round(len(complete_costs) / len(selected), 6),
            "mean_cost_usd": round(_mean(complete_costs), 9) if complete_costs else None,
            "mean_wall_seconds": round(_mean([run["wall_seconds"] for run in selected]), 6),
            "quality_score_per_usd": round(quality_mean / (cost_total / len(selected)), 6) if all_costs_known and cost_total > 0 else None,
            "cost_per_passing_run_usd": round(cost_total / passes, 9) if all_costs_known and passes else None,
            **token_fields,
        }

    baseline = "single-agent"
    comparisons: dict[str, Any] = {}
    for mode_index, mode in enumerate(spec["modes"]):
        if mode == baseline:
            continue
        quality_deltas: list[float] = []
        quality_weights: list[float] = []
        cost_deltas: list[float] = []
        all_costs_known = True
        wins = ties = 0
        for case in spec["cases"]:
            for replicate in range(spec["repetitions"]):
                base = by_key[(baseline, case["id"], replicate)]
                candidate = by_key[(mode, case["id"], replicate)]
                base_quality = quality_of(base, case)[0]
                candidate_quality = quality_of(candidate, case)[0]
                delta = candidate_quality - base_quality
                quality_deltas.append(delta)
                quality_weights.append(case.get("weight", 1))
                wins += delta > 0
                ties += delta == 0
                if base["cost_complete"] and candidate["cost_complete"]:
                    cost_deltas.append(cost_of(candidate, spec) - cost_of(base, spec))
                else:
                    all_costs_known = False
        comparisons[mode] = {
            "baseline": baseline,
            "paired_runs": len(quality_deltas),
            "quality_delta_mean": round(_weighted_mean(quality_deltas, quality_weights), 6),
            "quality_delta_mean_95ci": _bootstrap_ci(quality_deltas, samples, spec["seed"] + 100 + mode_index, weights=quality_weights),
            "quality_wins": wins,
            "quality_ties": ties,
            "cost_delta_mean_usd": round(_mean(cost_deltas), 9) if all_costs_known else None,
            "cost_delta_mean_usd_95ci": _bootstrap_ci(cost_deltas, samples, spec["seed"] + 200 + mode_index, digits=9) if all_costs_known else None,
        }
    return {"modes": mode_summary, "comparisons_to_single_agent": comparisons}


def markdown_report(result: dict[str, Any]) -> str:
    summary = result["summary"]
    lines = ["# Agent evaluation", "", "| Mode | Quality | Passes | Known cost | Cost coverage | Mean wall time | Quality / USD |",
             "| --- | ---: | ---: | ---: | ---: | ---: | ---: |"]
    for mode in result["mode_order"]:
        row = summary["modes"][mode]
        efficiency = "n/a" if row["quality_score_per_usd"] is None else f'{row["quality_score_per_usd"]:.3f}'
        lines.append(f'| {mode} | {100 * row["quality_mean"]:.1f}% | {row["passing_runs"]}/{row["runs"]} | ${row["known_cost_usd"]:.6f} | {100 * row["cost_coverage"]:.1f}% | {row["mean_wall_seconds"]:.3f}s | {efficiency} |')
    lines += ["", "## Paired differences from single-agent", "",
              "| Mode | Mean quality difference | Quality wins | Mean cost difference |",
              "| --- | ---: | ---: | ---: |"]
    for mode, comparison in summary["comparisons_to_single_agent"].items():
        cost = "n/a" if comparison["cost_delta_mean_usd"] is None else f'${comparison["cost_delta_mean_usd"]:+.6f}'
        lines.append(f'| {mode} | {100 * comparison["quality_delta_mean"]:+.1f} pp | {comparison["quality_wins"]}/{comparison["paired_runs"]} | {cost} |')
    lines += ["", "Quality includes zero scores for failed runs. Cost efficiency is omitted unless every run in that mode has complete cost capture.", ""]
    return "\n".join(lines)


def write_result(path: str | Path, result: dict[str, Any]) -> None:
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    Path(path).write_text(json.dumps(result, indent=2, sort_keys=True) + "\n")


def result_document(spec: dict[str, Any], raw_spec: bytes, runs: list[dict[str, Any]]) -> dict[str, Any]:
    return {"schema": RESULT_SCHEMA, "spec_sha256": hashlib.sha256(raw_spec).hexdigest(),
            "mode_order": spec["modes"], "runs": runs, "summary": summarize(spec, runs)}


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    run_parser = sub.add_parser("run", help="execute the balanced trial matrix")
    run_parser.add_argument("--spec", required=True)
    run_parser.add_argument("--runner", required=True)
    run_parser.add_argument("--runner-arg", action="append", default=[])
    run_parser.add_argument("--output", required=True)
    run_parser.add_argument("--markdown")
    summary_parser = sub.add_parser("summarize", help="validate and summarize existing result documents")
    summary_parser.add_argument("--spec", required=True)
    summary_parser.add_argument("--observations", required=True, nargs="+")
    summary_parser.add_argument("--output", required=True)
    summary_parser.add_argument("--markdown")
    args = parser.parse_args(argv)
    try:
        spec, raw_spec = load_spec(args.spec)
        if args.command == "run":
            spec_path = Path(args.spec).resolve()
            runs = []
            for mode in spec["modes"]:
                for case in spec["cases"]:
                    for replicate in range(spec["repetitions"]):
                        run = invoke_runner(args.runner, args.runner_arg, spec_path, spec, case, mode, replicate)
                        runs.append(validate_run(run, spec, expected_key=(mode, case["id"], replicate)))
        else:
            runs = []
            expected_hash = hashlib.sha256(raw_spec).hexdigest()
            for observation_path in args.observations:
                value = json.loads(Path(observation_path).read_text())
                if isinstance(value, dict) and "runs" in value:
                    if value.get("schema") != RESULT_SCHEMA:
                        raise EvaluationError(f"{observation_path}: result document has an invalid schema")
                    if value.get("spec_sha256") != expected_hash:
                        raise EvaluationError(f"{observation_path}: result document was produced from a different spec")
                    if not isinstance(value["runs"], list):
                        raise EvaluationError(f"{observation_path}: runs must be an array")
                    runs.extend(value["runs"])
                elif isinstance(value, list):
                    runs.extend(value)
                else:
                    runs.append(value)
        result = result_document(spec, raw_spec, runs)
        write_result(args.output, result)
        if args.markdown:
            Path(args.markdown).parent.mkdir(parents=True, exist_ok=True)
            Path(args.markdown).write_text(markdown_report(result))
        return 0
    except (EvaluationError, OSError, json.JSONDecodeError) as exc:
        print(f"evaluation error: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
