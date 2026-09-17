#!/usr/bin/env python3
"""Credential-free runner adapter for the example evaluation fixture.

This adapter does not execute agents. It turns measurements embedded in the
fixture spec into the JSON contract expected by evaluate_agents.py so the full
evaluation path can be tested locally.
"""

from __future__ import annotations

import argparse
import json
import sys
from pathlib import Path
from typing import Any


def fail(message: str) -> None:
    print(f"fixture runner error: {message}", file=sys.stderr)
    raise SystemExit(2)


def object_value(value: Any, label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        fail(f"{label} must be an object")
    return value


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--eval-spec", required=True)
    parser.add_argument("--eval-mode", required=True)
    parser.add_argument("--eval-case", required=True)
    parser.add_argument("--eval-replicate", required=True, type=int)
    parser.add_argument("--eval-seed", required=True, type=int)
    args = parser.parse_args()

    try:
        spec = object_value(json.loads(Path(args.eval_spec).read_text()), "spec")
    except (OSError, json.JSONDecodeError) as exc:
        fail(f"cannot read spec: {exc}")

    cases = spec.get("cases")
    if not isinstance(cases, list):
        fail("spec cases must be an array")
    try:
        case_index, case = next(
            (index, value)
            for index, value in enumerate(cases)
            if isinstance(value, dict) and value.get("id") == args.eval_case
        )
    except StopIteration:
        fail(f"unknown case {args.eval_case!r}")

    scores_by_mode = object_value(
        case.get("fixture_quality_by_mode"),
        f"case {args.eval_case!r} fixture_quality_by_mode",
    )
    scores = scores_by_mode.get(args.eval_mode)
    if not isinstance(scores, list) or not 0 <= args.eval_replicate < len(scores):
        fail(
            f"missing fixture scores for mode {args.eval_mode!r}, "
            f"replicate {args.eval_replicate}"
        )
    earned_by_name = object_value(
        scores[args.eval_replicate],
        f"case {args.eval_case!r} fixture scores",
    )
    checks = case.get("quality_checks")
    if not isinstance(checks, list):
        fail(f"case {args.eval_case!r} quality_checks must be an array")
    check_names = [check.get("name") for check in checks if isinstance(check, dict)]
    if len(check_names) != len(checks) or set(earned_by_name) != set(check_names):
        fail(f"case {args.eval_case!r} fixture scores must match its quality checks")

    fixture = object_value(spec.get("fixture"), "spec fixture")
    usage_by_mode = object_value(fixture.get("token_usage_by_mode"), "fixture token_usage_by_mode")
    usage = object_value(usage_by_mode.get(args.eval_mode), f"usage for mode {args.eval_mode!r}")
    input_tokens = usage.get("input_tokens")
    cached_input_tokens = usage.get("cached_input_tokens")
    output_tokens = usage.get("output_tokens")
    increments = (
        fixture.get("per_case_input_tokens", 0),
        fixture.get("per_case_output_tokens", 0),
        fixture.get("per_replicate_input_tokens", 0),
        fixture.get("per_replicate_output_tokens", 0),
    )
    if not all(isinstance(value, int) and not isinstance(value, bool) for value in (
        input_tokens, cached_input_tokens, output_tokens, *increments
    )):
        fail("fixture token counts and increments must be integers")

    result = {
        "status": "completed",
        "quality": [
            {"name": name, "earned": earned_by_name[name]} for name in check_names
        ],
        "model_calls": [
            {
                "model": fixture.get("model"),
                "input_tokens": input_tokens + case_index * increments[0]
                + args.eval_replicate * increments[2],
                "cached_input_tokens": cached_input_tokens,
                "output_tokens": output_tokens + case_index * increments[1]
                + args.eval_replicate * increments[3],
            }
        ],
        "non_model_cost_usd": 0,
        "cost_complete": True,
        "metadata": {
            "fixture": True,
            "received_seed": args.eval_seed,
        },
    }
    print(json.dumps(result, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
