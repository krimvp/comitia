#!/usr/bin/env python3
"""Runner adapter that grades per-trial artifacts with the Jev System One API.

This adapter does not execute agents. It reads the artifact an agent produced
for a trial from the spec (case-level ``artifacts_by_mode``), asks Jev one
``noul`` question per declared quality check over ``{task, artifact}``, and
prints the JSON measurement expected by evaluate_agents.py. A check passes when
its calibrated probability is above PASS_THRESHOLD. A missing key, a transport
fault, or a malformed answer is reported as a failed run, never as a pass.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import sys
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen


API_URL = "https://api.typesafe.ai/v1/systemone"
API_KEY_ENV = "TYPESAFE_API_KEY"
# The pricing key of the model requested; the model id Jev answers with is kept
# as evidence in the run metadata.
MODEL = "jev-latest"
TIMEOUT_SECONDS = 30
# A noul answer is a calibrated probability that the statement holds; strictly
# more likely than not earns the check's points, anything else earns zero.
PASS_THRESHOLD = 0.5


class RunnerError(Exception):
    pass


def object_value(value: Any, label: str) -> dict[str, Any]:
    if not isinstance(value, dict):
        raise RunnerError(f"{label} must be an object")
    return value


def load_case(spec_path: Path, case_id: str) -> dict[str, Any]:
    try:
        spec = object_value(json.loads(spec_path.read_text()), "spec")
    except (OSError, json.JSONDecodeError) as exc:
        raise RunnerError(f"cannot read spec: {exc}") from exc
    cases = spec.get("cases")
    if not isinstance(cases, list):
        raise RunnerError("spec cases must be an array")
    for value in cases:
        if isinstance(value, dict) and value.get("id") == case_id:
            return value
    raise RunnerError(f"unknown case {case_id!r}")


def check_names(case: dict[str, Any]) -> list[str]:
    checks = case.get("quality_checks")
    if not isinstance(checks, list):
        raise RunnerError("case quality_checks must be an array")
    return [object_value(check, "quality check").get("name") for check in checks]


def load_artifact(spec_path: Path, case: dict[str, Any], mode: str) -> str:
    artifacts = object_value(case.get("artifacts_by_mode"), "case artifacts_by_mode")
    artifact = artifacts.get(mode)
    if isinstance(artifact, str):
        return artifact
    if isinstance(artifact, dict) and isinstance(artifact.get("path"), str):
        base = spec_path.parent.resolve()
        path = (base / artifact["path"]).resolve()
        if not path.is_relative_to(base):
            raise RunnerError(f"artifact path for mode {mode!r} leaves the spec directory")
        try:
            return path.read_text()
        except OSError as exc:
            raise RunnerError(f"cannot read artifact for mode {mode!r}: {exc}") from exc
    raise RunnerError(f"missing artifact for mode {mode!r}")


def questions_for(case: dict[str, Any]) -> dict[str, dict[str, Any]]:
    questions: dict[str, dict[str, Any]] = {}
    for check in case["quality_checks"]:
        name = check["name"]
        instructions = check.get("instructions")
        if not isinstance(instructions, str) or not instructions.strip():
            instructions = f"The artifact satisfies the quality check named {name!r} for the task."
        questions[name] = {
            "type": "noul",
            "instructions": instructions,
            "criteria": {
                "true": "The artifact shows this is done, with concrete evidence.",
                "false": "The artifact does not show it, only claims it, or contradicts it.",
            },
        }
    return questions


def ask_jev(api_key: str, state: dict[str, Any], questions: dict[str, Any]) -> dict[str, Any]:
    body = json.dumps({"model": MODEL, "state": state, "questions": questions}).encode()
    request = Request(API_URL, data=body, method="POST", headers={
        "Authorization": f"Bearer {api_key}",
        "Content-Type": "application/json",
    })
    try:
        with urlopen(request, timeout=TIMEOUT_SECONDS) as response:
            raw = response.read()
    except HTTPError as exc:
        raise RunnerError(f"jev returned HTTP {exc.code}") from exc
    except (URLError, OSError, TimeoutError) as exc:
        raise RunnerError(f"jev request failed: {exc}") from exc
    try:
        return object_value(json.loads(raw), "jev response")
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        raise RunnerError(f"jev response is not JSON: {exc}") from exc


def probabilities_from(response: dict[str, Any], names: list[str]) -> dict[str, float]:
    answers = object_value(response.get("answers"), "jev answers")
    probabilities: dict[str, float] = {}
    for name in names:
        answer = object_value(answers.get(name), f"jev answer {name!r}")
        value = answer.get("noul")
        if answer.get("type") != "noul" or isinstance(value, bool) \
                or not isinstance(value, (int, float)) or not 0 <= value <= 1:
            raise RunnerError(f"jev answer {name!r} is not a noul probability")
        probabilities[name] = float(value)
    return probabilities


def usage_from(response: dict[str, Any]) -> dict[str, int]:
    usage = object_value(response.get("usage"), "jev usage")
    tokens: dict[str, int] = {}
    for field in ("input_tokens", "output_tokens"):
        value = usage.get(field)
        if not isinstance(value, int) or isinstance(value, bool) or value < 0:
            raise RunnerError(f"jev usage {field} must be a non-negative integer")
        tokens[field] = value
    return tokens


def failed_run(names: list[str], error: str, *, cost_complete: bool) -> dict[str, Any]:
    return {
        "status": "failed",
        "quality": [{"name": name, "earned": 0} for name in names],
        "model_calls": [],
        "non_model_cost_usd": 0,
        "cost_complete": cost_complete,
        "error": error,
        "metadata": {"grader": "jev", "requested_model": MODEL},
    }


def grade(spec_path: Path, mode: str, case_id: str, seed: int) -> dict[str, Any]:
    names: list[str] = []
    requested = False
    try:
        case = load_case(spec_path, case_id)
        names = check_names(case)
        artifact = load_artifact(spec_path, case, mode)
        api_key = os.environ.get(API_KEY_ENV, "")
        if not api_key:
            raise RunnerError(f"{API_KEY_ENV} is not set")
        state = {"task": case.get("task", ""), "artifact": artifact}
        requested = True
        response = ask_jev(api_key, state, questions_for(case))
        probabilities = probabilities_from(response, names)
        usage = usage_from(response)
    except RunnerError as exc:
        # Nothing was sent before the fault, so the (empty) cost is complete;
        # after a request the usage is unknown and the cost is incomplete.
        return failed_run(names, str(exc), cost_complete=not requested)
    return {
        "status": "completed",
        "quality": [
            {"name": check["name"],
             "earned": check["points"] if probabilities[check["name"]] > PASS_THRESHOLD else 0}
            for check in case["quality_checks"]
        ],
        "model_calls": [{"model": MODEL, "input_tokens": usage["input_tokens"],
                         "cached_input_tokens": 0, "output_tokens": usage["output_tokens"]}],
        "non_model_cost_usd": 0,
        "cost_complete": True,
        "metadata": {
            "grader": "jev",
            "requested_model": MODEL,
            "model": response.get("model"),
            "probabilities": probabilities,
            "pass_threshold": PASS_THRESHOLD,
            "artifact_sha256": hashlib.sha256(artifact.encode()).hexdigest(),
            "received_seed": seed,
        },
    }


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--eval-spec", required=True)
    parser.add_argument("--eval-mode", required=True)
    parser.add_argument("--eval-case", required=True)
    parser.add_argument("--eval-replicate", required=True, type=int)
    parser.add_argument("--eval-seed", required=True, type=int)
    args = parser.parse_args(argv)
    result = grade(Path(args.eval_spec), args.eval_mode, args.eval_case, args.eval_seed)
    print(json.dumps(result, sort_keys=True))
    if result["status"] != "completed":
        print(f"jev runner error: {result['error']}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
