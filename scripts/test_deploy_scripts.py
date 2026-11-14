"""One-host deploy script regressions with a fake comitia binary and a temporary COMITIA_HOME.

Run: python3 -m unittest scripts/test_deploy_scripts.py
"""
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCRIPTS = ROOT/'scripts'
# The deployment scripts live beside the nginx configs they install; deploy/verify
# holds the live-deployment checks from docs/history/RUNBOOK.md.
DEPLOY = ROOT/'deploy'

FAKE = '''#!/usr/bin/env python3
import json, os, sys
with open(os.environ["FAKE_LOG"], "a") as log:
    log.write(json.dumps(sys.argv[1:]) + "\\n")
cmd = sys.argv[1]
cred = {"principal": {"ID": "principal-1"},
        "credential": {"Token": "token-0123456789", "Session": {"ID": "session-1", "ExpiresAt": "2027-01-01T00:00:00Z"}},
        "grants": [{"ContextID": "context-1", "Permissions": 15, "ExpiresAt": "2027-01-01T00:00:00Z"}]}
print(json.dumps({"workspace-create": {"ID": "workspace-1", "Name": "harness-agents"},
                  "context-create": {"ID": "context-1", "Name": "handoff"}}.get(cmd, cred)), end="")
'''


def mode(path):
    return stat.S_IMODE(path.stat().st_mode)


class DeployScriptsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        base = Path(self.tmp.name)
        bin_dir = base/'bin'
        bin_dir.mkdir()
        self.fake = bin_dir/'comitia'
        self.fake.write_text(FAKE)
        self.fake.chmod(0o755)
        self.log = base/'calls.log'
        self.home = base/'state'
        self.home.mkdir(mode=0o755)
        self.home.chmod(0o755)
        self.owner = {'principal': {'ID': 'owner'}, 'credential': {'Token': 'owner-token', 'Session': {'ID': 'owner-session', 'ExpiresAt': 'x'}}}
        (self.home/'owner.json').write_text(json.dumps(self.owner))
        (self.home/'ids.env').write_text('COMITIA_WORKSPACE_ID=workspace-1\nCOMITIA_CONTEXT_ID=context-1\n')
        self.env = {'PATH': f'{bin_dir}{os.pathsep}{os.environ["PATH"]}', 'FAKE_LOG': str(self.log),
                    'COMITIA_BIN': str(self.fake), 'COMITIA_HOME': str(self.home),
                    'COMITIA_DB': str(base/'comitia.db'), 'COMITIA_PUBLIC_HOST': 'comitia.example.com'}

    def run_script(self, *argv):
        # A permissive umask proves the scripts do not rely on the caller's.
        command = ['bash', '-c', 'umask 022; exec "$@"', '_', *argv]
        return subprocess.run(command, env=self.env, capture_output=True, text=True, timeout=30)

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def assertPrivate(self, path):
        self.assertEqual(mode(path), 0o600, path)
        self.assertEqual(mode(path.parent), 0o700, path.parent)

    def test_delegate_rejects_names_that_escape_sessions(self):
        for script in ('deploy-delegate.sh', 'comitia-add-harness.sh'):
            for name in ('../owner', '..', '.hidden', 'a/b', '/tmp/x', 'owner\n'):
                with self.subTest(script=script, name=name):
                    result = self.run_script('bash', str(DEPLOY/script), name)
                    self.assertNotEqual(result.returncode, 0, result.stdout)
                    self.assertIn('invalid harness name', result.stderr)
        self.assertEqual(self.calls(), [], 'nothing may be minted for a rejected name')
        self.assertEqual(json.loads((self.home/'owner.json').read_text()), self.owner)
        self.assertFalse((self.home/'sessions').exists() and any((self.home/'sessions').iterdir()))

    def test_add_harness_refuses_owner(self):
        result = self.run_script('bash', str(DEPLOY/'comitia-add-harness.sh'), 'owner')
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(self.calls(), [])

    def test_delegate_writes_session_privately(self):
        (self.home/'sessions').mkdir(mode=0o755)
        (self.home/'sessions').chmod(0o755)
        stale = self.home/'sessions/codex.json'
        stale.write_text('{}')
        stale.chmod(0o644)
        result = self.run_script('bash', str(DEPLOY/'comitia-add-harness.sh'), 'codex.v2_x-1', 'read', '1')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn('https://comitia.example.com:8443/mcp', result.stdout)
        self.assertEqual(self.calls()[0][:3], ['delegate', '--name', 'codex.v2_x-1'])
        session = self.home/'sessions/codex.v2_x-1.json'
        self.assertEqual(json.loads(session.read_text())['credential']['Token'], 'token-0123456789')
        self.assertPrivate(session)
        self.assertEqual(mode(self.home), 0o700)
        result = self.run_script('bash', str(DEPLOY/'deploy-delegate.sh'), 'codex')
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertPrivate(stale)
        self.assertEqual([p.name for p in (self.home/'sessions').iterdir() if p.name.startswith('.')], [])

    def test_bootstrap_and_workspace_write_private_files(self):
        (self.home/'owner.json').unlink()
        (self.home/'ids.env').unlink()
        (self.home/'bootstrap.secret').write_text('bootstrap')
        result = self.run_script('bash', str(DEPLOY/'deploy-bootstrap.sh'))
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertPrivate(self.home/'owner.json')
        result = self.run_script('bash', str(DEPLOY/'deploy-workspace.sh'))
        self.assertEqual(result.returncode, 0, result.stderr)
        for name in ('workspace.json', 'context.json', 'ids.env'):
            self.assertEqual(mode(self.home/name), 0o600, name)
        self.assertIn('COMITIA_CONTEXT_ID=context-1', (self.home/'ids.env').read_text())
        self.assertEqual([c[0] for c in self.calls()], ['bootstrap', 'workspace-create', 'context-create'])

    def test_show_credentials_writes_handout_privately(self):
        self.home.chmod(0o700)
        (self.home/'sessions').mkdir(mode=0o700)
        result = self.run_script(sys.executable, str(DEPLOY/'show-credentials.py'))
        self.assertEqual(result.returncode, 0, result.stderr)
        handout = self.home/'credentials.txt'
        self.assertPrivate(handout)
        self.assertIn('https://comitia.example.com:8443/mcp', handout.read_text())
        self.assertIn('owner-token', handout.read_text())

    def test_deploy_files_name_only_documentation_hosts(self):
        allowed = re.compile(r'^(127\.|0\.0\.0\.0$|192\.0\.2\.|198\.51\.100\.|203\.0\.113\.)')
        deploy = [p for p in DEPLOY.rglob('*') if p.is_file() and '__pycache__' not in p.parts]
        self.assertTrue(any(p.parent == DEPLOY/'verify' for p in deploy), 'deploy/verify scripts are covered')
        paths = [*SCRIPTS.glob('*.sh'), *SCRIPTS.glob('*.py'), *deploy, *(ROOT/'docs/history').glob('*.md')]
        for path in paths:
            text = path.read_text()
            for address in re.findall(r'\b\d{1,3}(?:\.\d{1,3}){3}\b', text):
                self.assertRegex(address, allowed, f'{path.name} names a real address')
        for conf in DEPLOY.glob('*.nginx.conf'):
            names = set(re.findall(r'^\s*server_name\s+([^;]+);', conf.read_text(), re.M))
            self.assertEqual(names, {'comitia.example.com'}, conf.name)


if __name__ == '__main__':
    unittest.main()
