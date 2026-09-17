import contextlib
import copy
import importlib.util
import io
import json
import os
import tempfile
import unittest
from pathlib import Path
from urllib.error import HTTPError


MODULE_PATH = Path(__file__).with_name("evaluation_jev_runner.py")
EVALUATOR_PATH = MODULE_PATH.with_name("evaluate_agents.py")
JEV_SPEC_PATH = MODULE_PATH.parent.parent / "examples" / "evaluation" / "jev-benchmark.json"
# Captured before setUp replaces the key with a fake one for the offline tests.
LIVE_KEY = os.environ.get("TYPESAFE_API_KEY", "")


def load_module(path):
    spec = importlib.util.spec_from_file_location(path.stem, path)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


runner = load_module(MODULE_PATH)
evaluate_agents = load_module(EVALUATOR_PATH)

SPEC = {
    "schema": "comitia.eval-spec.v1",
    "seed": 7,
    "repetitions": 1,
    "bootstrap_samples": 100,
    "runner_timeout_seconds": 60,
    "modes": ["single-agent", "multi-agent", "comitia"],
    "cases": [{
        "id": "repair",
        "task": "Fix the bug and add a regression test.",
        "pass_score": 8,
        "quality_checks": [
            {"name": "tests", "points": 8, "instructions": "A regression test was added and passes."},
            {"name": "review", "points": 2},
        ],
        "artifacts_by_mode": {
            "single-agent": "Added TestRegression; go test ok.",
            "multi-agent": {"path": "artifacts/multi.txt"},
        },
    }],
    "pricing_usd_per_million_tokens": {
        runner.MODEL: {"input": 0.5, "cached_input": 0.5, "output": 2.0}
    },
}


class FakeResponse(io.BytesIO):
    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.close()


def jev_response(tests, review, model="jev-1.13.0"):
    return {
        "model": model,
        "answers": {"tests": {"type": "noul", "noul": tests}, "review": {"type": "noul", "noul": review}},
        "usage": {"input_tokens": 456, "output_tokens": 58},
    }


class JevRunnerTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        root = Path(self.directory.name)
        self.spec_path = root / "spec.json"
        self.spec_path.write_text(json.dumps(SPEC))
        (root / "artifacts").mkdir()
        (root / "artifacts" / "multi.txt").write_text("Reviewer: no test was added.")
        self.requests = []
        self.addCleanup(setattr, runner, "urlopen", runner.urlopen)
        self.addCleanup(os.environ.pop, runner.API_KEY_ENV, None)
        os.environ[runner.API_KEY_ENV] = "test-key"

    def fake_urlopen(self, reply):
        def urlopen(request, timeout=None):
            self.requests.append((request, timeout))
            if isinstance(reply, Exception):
                raise reply
            return FakeResponse(reply if isinstance(reply, bytes) else json.dumps(reply).encode())
        runner.urlopen = urlopen

    def run_main(self, mode="single-agent", case="repair"):
        argv = ["--eval-spec", str(self.spec_path), "--eval-mode", mode, "--eval-case", case,
                "--eval-replicate", "0", "--eval-seed", "42"]
        with contextlib.redirect_stdout(io.StringIO()) as stdout, contextlib.redirect_stderr(io.StringIO()):
            rc = runner.main(argv)
        lines = stdout.getvalue().splitlines()
        self.assertEqual(len(lines), 1)
        return rc, json.loads(lines[0])

    def assert_failed(self, rc, result, message, *, cost_complete):
        self.assertEqual(rc, 1)
        self.assertEqual(result["status"], "failed")
        self.assertEqual(result["quality"], [{"name": "tests", "earned": 0}, {"name": "review", "earned": 0}])
        self.assertEqual(result["model_calls"], [])
        self.assertIs(result["cost_complete"], cost_complete)
        self.assertIn(message, result["error"])
        run = dict(result, mode="single-agent", case_id="repair", replicate=0, wall_seconds=0.1,
                   seed=evaluate_agents.trial_seed(SPEC["seed"], "single-agent", "repair", 0))
        evaluate_agents.validate_run(run, SPEC)

    def test_pass_awards_points_and_records_evidence(self):
        self.fake_urlopen(jev_response(0.94, 0.51))
        rc, result = self.run_main()
        self.assertEqual(rc, 0)
        self.assertEqual(result["status"], "completed")
        self.assertEqual(result["quality"], [{"name": "tests", "earned": 8}, {"name": "review", "earned": 2}])
        self.assertEqual(result["model_calls"], [{"model": "jev-latest", "input_tokens": 456,
                                                  "cached_input_tokens": 0, "output_tokens": 58}])
        self.assertTrue(result["cost_complete"])
        self.assertEqual(result["metadata"]["model"], "jev-1.13.0")
        self.assertEqual(result["metadata"]["probabilities"], {"tests": 0.94, "review": 0.51})
        self.assertEqual(result["metadata"]["received_seed"], 42)

        request, timeout = self.requests[0]
        self.assertEqual(timeout, runner.TIMEOUT_SECONDS)
        self.assertEqual(request.full_url, runner.API_URL)
        self.assertEqual(request.get_header("Authorization"), "Bearer test-key")
        body = json.loads(request.data)
        self.assertEqual(body["model"], "jev-latest")
        self.assertEqual(body["state"], {"task": SPEC["cases"][0]["task"], "artifact": "Added TestRegression; go test ok."})
        self.assertEqual(set(body["questions"]), {"tests", "review"})
        self.assertEqual(body["questions"]["tests"]["instructions"], "A regression test was added and passes.")
        self.assertEqual(body["questions"]["review"]["type"], "noul")

    def test_fail_awards_zero_at_or_below_threshold_and_reads_artifact_file(self):
        self.fake_urlopen(jev_response(0.12, 0.5))
        rc, result = self.run_main(mode="multi-agent")
        self.assertEqual(rc, 0)
        self.assertEqual(result["status"], "completed")
        self.assertEqual(result["quality"], [{"name": "tests", "earned": 0}, {"name": "review", "earned": 0}])
        self.assertEqual(json.loads(self.requests[0][0].data)["state"]["artifact"], "Reviewer: no test was added.")

    def test_missing_artifact_fails_without_a_request(self):
        self.fake_urlopen(jev_response(0.9, 0.9))
        rc, result = self.run_main(mode="comitia")
        self.assert_failed(rc, result, "missing artifact for mode 'comitia'", cost_complete=True)
        self.assertEqual(self.requests, [])

    def test_missing_key_fails_without_a_request(self):
        del os.environ[runner.API_KEY_ENV]
        self.fake_urlopen(jev_response(0.9, 0.9))
        rc, result = self.run_main()
        self.assert_failed(rc, result, "TYPESAFE_API_KEY is not set", cost_complete=True)
        self.assertEqual(self.requests, [])

    def test_faults_fail_closed_with_incomplete_cost(self):
        malformed = copy.deepcopy(jev_response(0.9, 0.9))
        malformed["answers"]["review"] = {"type": "choice", "choice": "a"}
        out_of_range = copy.deepcopy(jev_response(0.9, 0.9))
        out_of_range["answers"]["tests"]["noul"] = 1.5
        missing_usage = copy.deepcopy(jev_response(0.9, 0.9))
        del missing_usage["usage"]
        faults = [
            (HTTPError(runner.API_URL, 401, "Unauthorized", {}, None), "HTTP 401"),
            (HTTPError(runner.API_URL, 529, "Overloaded", {}, None), "HTTP 529"),
            (TimeoutError("timed out"), "request failed"),
            (b"not json", "not JSON"),
            (malformed, "not a noul probability"),
            (out_of_range, "not a noul probability"),
            (missing_usage, "jev usage must be an object"),
        ]
        for reply, message in faults:
            with self.subTest(message=message):
                self.fake_urlopen(reply)
                rc, result = self.run_main()
                self.assert_failed(rc, result, message, cost_complete=False)

    def test_example_spec_grades_through_the_evaluator(self):
        # Every check of every mode "passes" here; only the wiring is under test.
        def urlopen(request, timeout=None):
            names = json.loads(request.data)["questions"]
            return FakeResponse(json.dumps({
                "model": "jev-1.13.0",
                "answers": {name: {"type": "noul", "noul": 0.9} for name in names},
                "usage": {"input_tokens": 500, "output_tokens": 50},
            }).encode())
        runner.urlopen = urlopen
        # The evaluator would run the adapter in a subprocess, out of reach of
        # the fake, so grade in-process and validate against the loaded spec.
        spec, _ = evaluate_agents.load_spec(JEV_SPEC_PATH)
        for case in spec["cases"]:
            for mode in spec["modes"]:
                result = runner.grade(JEV_SPEC_PATH, mode, case["id"], 1)
                run = dict(result, mode=mode, case_id=case["id"], replicate=0, wall_seconds=0.1,
                           seed=evaluate_agents.trial_seed(spec["seed"], mode, case["id"], 0))
                evaluate_agents.validate_run(run, spec)
                self.assertEqual(result["status"], "completed")
                self.assertEqual(sum(check["earned"] for check in result["quality"]),
                                 sum(check["points"] for check in case["quality_checks"]))

    @unittest.skipUnless(os.environ.get("COMITIA_LIVE") == "1" and LIVE_KEY,
                         "set COMITIA_LIVE=1 and TYPESAFE_API_KEY for the live grader run")
    def test_live_example_matches_expected_pattern(self):
        os.environ[runner.API_KEY_ENV] = LIVE_KEY
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "result.json"
            rc = evaluate_agents.main(["run", "--spec", str(JEV_SPEC_PATH), "--runner", str(MODULE_PATH),
                                       "--output", str(output)])
            self.assertEqual(rc, 0)
            summary = json.loads(output.read_text())["summary"]["modes"]
            self.assertEqual(summary["comitia"]["quality_mean"], 1.0)
            self.assertLess(summary["single-agent"]["quality_mean"], summary["multi-agent"]["quality_mean"])
            self.assertEqual(summary["comitia"]["cost_coverage"], 1.0)


if __name__ == "__main__":
    unittest.main()
