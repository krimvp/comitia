import importlib.util
import copy
import contextlib
import io
import json
import stat
import sys
import tempfile
import unittest
from pathlib import Path


MODULE_PATH = Path(__file__).with_name("evaluate_agents.py")
REPOSITORY_ROOT = MODULE_PATH.parent.parent
FIXTURE_SPEC_PATH = REPOSITORY_ROOT / "examples" / "evaluation" / "fixture-benchmark.json"
FIXTURE_RUNNER_PATH = MODULE_PATH.with_name("evaluation_fixture_runner.py")
SPEC = importlib.util.spec_from_file_location("evaluate_agents", MODULE_PATH)
evaluate_agents = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(evaluate_agents)


def benchmark_spec(repetitions=1):
    return {
        "schema": "comitia.eval-spec.v1",
        "seed": 73,
        "repetitions": repetitions,
        "bootstrap_samples": 100,
        "runner_timeout_seconds": 10,
        "modes": ["single-agent", "multi-agent", "comitia"],
        "cases": [{
            "id": "repair",
            "weight": 1,
            "pass_score": 8,
            "quality_checks": [
                {"name": "tests", "points": 8},
                {"name": "review", "points": 2},
            ],
        }],
        "pricing_usd_per_million_tokens": {
            "test-model": {"input": 2, "cached_input": 0.5, "output": 8}
        },
    }


def measured_run(spec, mode, earned, *, cost_complete=True):
    return {
        "mode": mode,
        "case_id": "repair",
        "replicate": 0,
        "seed": evaluate_agents.trial_seed(spec["seed"], mode, "repair", 0),
        "status": "completed",
        "wall_seconds": 1.25,
        "quality": [{"name": "tests", "earned": earned}, {"name": "review", "earned": 1}],
        "model_calls": [{"model": "test-model", "input_tokens": 1000,
                         "cached_input_tokens": 200, "output_tokens": 100}],
        "non_model_cost_usd": 0.001,
        "cost_complete": cost_complete,
    }


class EvaluationTests(unittest.TestCase):
    def test_weighted_quality_is_consistent_across_metrics(self):
        spec = benchmark_spec()
        second = copy.deepcopy(spec["cases"][0])
        second.update(id="second", weight=9)
        spec["cases"].append(second)
        runs = []
        for mode in spec["modes"]:
            for case in spec["cases"]:
                run = measured_run(spec, mode, 0)
                run["case_id"] = case["id"]
                run["seed"] = evaluate_agents.trial_seed(spec["seed"], mode, case["id"], 0)
                passed = mode != "single-agent" and case["id"] == "second"
                run["quality"] = [{"name": "tests", "earned": 8 if passed else 0},
                                  {"name": "review", "earned": 2 if passed else 0}]
                runs.append(run)
        summary = evaluate_agents.summarize(spec, runs)
        mode = summary["modes"]["comitia"]
        self.assertEqual(mode["quality_mean"], 0.9)
        self.assertEqual(summary["comparisons_to_single_agent"]["comitia"]["quality_delta_mean"], 0.9)
        self.assertAlmostEqual(mode["quality_score_per_usd"], 0.9 / 0.0035, places=5)

    def test_malformed_runner_measurements_remain_in_matrix(self):
        outputs = ["{}", "not JSON", "{\"status\": []}"]
        valid = measured_run(benchmark_spec(), "single-agent", 5)
        for path in (("quality", 0, "name"), ("model_calls", 0, "model")):
            malformed = copy.deepcopy(valid)
            malformed[path[0]][path[1]][path[2]] = []
            outputs.append(json.dumps(malformed))
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            spec_path = root / "spec.json"
            spec_path.write_text(json.dumps(benchmark_spec()))
            for output in outputs:
                with self.subTest(output=output):
                    runner = root / "runner.py"
                    runner.write_text("import sys\nsys.stdout.write(" + repr(output) + ")\n")
                    result_path = root / "result.json"
                    rc = evaluate_agents.main([
                        "run", "--spec", str(spec_path), "--runner", sys.executable,
                        "--runner-arg", str(runner), "--output", str(result_path),
                    ])
                    self.assertEqual(rc, 0)
                    result = json.loads(result_path.read_text())
                    self.assertEqual(len(result["runs"]), 3)
                    self.assertTrue(all(run["status"] == "harness_error" for run in result["runs"]))
                    self.assertEqual(result["summary"]["modes"]["comitia"]["quality_mean"], 0)
                    self.assertEqual(result["summary"]["modes"]["comitia"]["cost_coverage"], 0)

    def test_runner_failures_are_zero_quality_with_incomplete_cost(self):
        spec = benchmark_spec()
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            runner = root / "runner.py"
            cases = [
                ("import sys\nsys.stdout.buffer.write(b'\\xff')", "harness_error"),
                ("import sys\nsys.stderr.write('x' * (4 * 1024 * 1024 + 1))", "harness_error"),
                ("import sys\nprint(" + repr(json.dumps(measured_run(spec, "single-agent", 5))) + ")\nsys.exit(1)", "failed"),
            ]
            for source, status in cases:
                with self.subTest(status=status, source=source[:40]):
                    runner.write_text(source)
                    run = evaluate_agents.invoke_runner(sys.executable, [str(runner)], root / "spec.json",
                                                        spec, spec["cases"][0], "single-agent", 0)
                    evaluate_agents.validate_run(run, spec)
                    self.assertEqual(run["status"], status)
                    self.assertFalse(run["cost_complete"])
                    self.assertTrue(all(check["earned"] == 0 for check in run["quality"]))

    def test_summarize_rejects_report_from_changed_spec(self):
        spec = benchmark_spec()
        runs = [measured_run(spec, mode, 5) for mode in spec["modes"]]
        raw_spec = json.dumps(spec).encode()
        result = evaluate_agents.result_document(spec, raw_spec, runs)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            spec_path = root / "spec.json"
            # Even whitespace changes invalidate the exact-byte fingerprint.
            spec_path.write_bytes(raw_spec + b"\n")
            observations = root / "observations.json"
            observations.write_text(json.dumps(result))
            with contextlib.redirect_stderr(io.StringIO()) as stderr:
                rc = evaluate_agents.main(["summarize", "--spec", str(spec_path),
                                           "--observations", str(observations),
                                           "--output", str(root / "result.json")])
            self.assertEqual(rc, 2)
            self.assertIn("different spec", stderr.getvalue())

    def test_large_integer_and_non_string_modes_are_validation_errors_not_crashes(self):
        self.assertEqual(evaluate_agents._number(10 ** 1000, "large"), 10 ** 1000)
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "spec.json"
            spec = benchmark_spec()
            spec["modes"] = ["single-agent", "multi-agent", {}]
            path.write_text(json.dumps(spec))
            with self.assertRaisesRegex(evaluate_agents.EvaluationError, "modes must"):
                evaluate_agents.load_spec(path)

    def test_cost_and_paired_quality_summary(self):
        spec = benchmark_spec()
        runs = [measured_run(spec, "single-agent", 5), measured_run(spec, "multi-agent", 7),
                measured_run(spec, "comitia", 8)]
        summary = evaluate_agents.summarize(spec, runs)

        # (800 uncached * $2 + 200 cached * $0.50 + 100 output * $8) / 1m,
        # plus $0.001 of explicitly reported non-model cost.
        self.assertAlmostEqual(summary["modes"]["single-agent"]["known_cost_usd"], 0.0035)
        self.assertEqual(summary["modes"]["comitia"]["quality_mean"], 0.9)
        self.assertEqual(summary["comparisons_to_single_agent"]["comitia"]["quality_delta_mean"], 0.3)
        self.assertEqual(summary["modes"]["comitia"]["passing_runs"], 1)

    def test_case_weights_apply_to_quality_deltas_and_efficiency(self):
        spec = benchmark_spec()
        spec["cases"] = [
            {"id": "rare", "weight": 1, "pass_score": 10,
             "quality_checks": [{"name": "score", "points": 10}]},
            {"id": "common", "weight": 9, "pass_score": 10,
             "quality_checks": [{"name": "score", "points": 10}]},
        ]
        runs = []
        for mode in spec["modes"]:
            for case in spec["cases"]:
                earned = 10 if mode != "single-agent" and case["id"] == "rare" else 0
                runs.append({
                    "mode": mode, "case_id": case["id"], "replicate": 0,
                    "seed": evaluate_agents.trial_seed(spec["seed"], mode, case["id"], 0),
                    "status": "completed", "wall_seconds": 1,
                    "quality": [{"name": "score", "earned": earned}],
                    "model_calls": [], "non_model_cost_usd": 0.01, "cost_complete": True,
                })
        summary = evaluate_agents.summarize(spec, runs)
        self.assertEqual(summary["modes"]["comitia"]["quality_mean"], 0.1)
        self.assertEqual(summary["comparisons_to_single_agent"]["comitia"]["quality_delta_mean"], 0.1)
        self.assertEqual(summary["modes"]["comitia"]["quality_score_per_usd"], 10)

    def test_incomplete_cost_is_visible_and_disables_efficiency(self):
        spec = benchmark_spec()
        runs = [measured_run(spec, "single-agent", 5), measured_run(spec, "multi-agent", 7),
                measured_run(spec, "comitia", 8, cost_complete=False)]
        summary = evaluate_agents.summarize(spec, runs)
        self.assertEqual(summary["modes"]["comitia"]["cost_coverage"], 0)
        self.assertIsNone(summary["modes"]["comitia"]["quality_score_per_usd"])
        self.assertIsNone(summary["comparisons_to_single_agent"]["comitia"]["cost_delta_mean_usd"])

    def test_rejects_unbalanced_matrix(self):
        spec = benchmark_spec()
        with self.assertRaisesRegex(evaluate_agents.EvaluationError, "unbalanced"):
            evaluate_agents.summarize(spec, [measured_run(spec, "single-agent", 5)])

    def test_run_command_executes_every_mode_and_writes_reports(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            spec_path = root / "spec.json"
            spec_path.write_text(json.dumps(benchmark_spec()))
            runner = root / "runner.py"
            runner.write_text("""#!/usr/bin/env python3
import argparse, json
p = argparse.ArgumentParser()
p.add_argument('--eval-spec'); p.add_argument('--eval-mode'); p.add_argument('--eval-case')
p.add_argument('--eval-replicate'); p.add_argument('--eval-seed'); a = p.parse_args()
score = {'single-agent': 5, 'multi-agent': 7, 'comitia': 8}[a.eval_mode]
print(json.dumps({'status':'completed','quality':[{'name':'tests','earned':score},{'name':'review','earned':1}],
 'model_calls':[{'model':'test-model','input_tokens':1000,'cached_input_tokens':0,'output_tokens':100}],
 'non_model_cost_usd':0,'cost_complete':True}))
""")
            runner.chmod(runner.stat().st_mode | stat.S_IXUSR)
            output = root / "result.json"
            report = root / "report.md"
            rc = evaluate_agents.main(["run", "--spec", str(spec_path), "--runner", str(runner),
                                       "--output", str(output), "--markdown", str(report)])
            self.assertEqual(rc, 0)
            result = json.loads(output.read_text())
            self.assertEqual(len(result["runs"]), 3)
            self.assertEqual(result["summary"]["modes"]["comitia"]["quality_mean"], 0.9)
            self.assertIn("Paired differences", report.read_text())

    def test_summarize_rejects_result_document_from_another_spec(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            spec_path = root / "spec.json"
            spec_path.write_text(json.dumps(benchmark_spec()))
            observations = root / "observations.json"
            observations.write_text(json.dumps({"schema": evaluate_agents.RESULT_SCHEMA,
                                                "spec_sha256": "0" * 64, "runs": []}))
            rc = evaluate_agents.main(["summarize", "--spec", str(spec_path),
                                       "--observations", str(observations),
                                       "--output", str(root / "result.json")])
            self.assertEqual(rc, 2)

    def test_checked_in_fixture_runs_without_credentials(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "result.json"
            report = Path(directory) / "report.md"
            rc = evaluate_agents.main([
                "run",
                "--spec", str(FIXTURE_SPEC_PATH),
                "--runner", str(FIXTURE_RUNNER_PATH),
                "--output", str(output),
                "--markdown", str(report),
            ])

            self.assertEqual(rc, 0)
            result = json.loads(output.read_text())
            self.assertEqual(len(result["runs"]), 12)
            self.assertTrue(all(run["status"] == "completed" for run in result["runs"]))
            self.assertTrue(all(run["cost_complete"] for run in result["runs"]))
            self.assertEqual(result["summary"]["modes"]["comitia"]["quality_mean"], 0.983333)
            self.assertEqual(
                result["summary"]["comparisons_to_single_agent"]["comitia"]["quality_wins"],
                4,
            )
            self.assertIn("| comitia | 98.3% | 4/4 |", report.read_text())


if __name__ == "__main__":
    unittest.main()
