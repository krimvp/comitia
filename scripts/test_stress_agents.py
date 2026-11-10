import inspect
from pathlib import Path
import subprocess
import sys
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import stress_agents


class CallTimeoutTest(unittest.TestCase):
    def test_timeout_outlasts_cli_lock_wait(self):
        # The CLI waits up to 15 s for the SQLite write lock.
        self.assertGreater(inspect.signature(stress_agents.call).parameters['timeout'].default, 15)

    def test_timeout_names_dedupe_key(self):
        with mock.patch.object(stress_agents.subprocess, 'run', side_effect=subprocess.TimeoutExpired('comitia', 1)):
            with self.assertRaisesRegex(RuntimeError, r'^send w3-m7 timed out$'):
                stress_agents.call('comitia', 'db', 'cred', 'send', '--context-id', 'c', '--dedupe-key', 'w3-m7', '--body', 'w3-m7')
            with self.assertRaisesRegex(RuntimeError, r'^task-create timed out$'):
                stress_agents.call('comitia', 'db', 'cred', 'task-create', '--context-id', 'c')


if __name__ == '__main__':
    unittest.main()
