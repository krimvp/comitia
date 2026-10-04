"""Credential lifecycle regressions against a real database and temporary HOME."""
import datetime
import contextlib
import io
import json
from pathlib import Path
import sqlite3
import runpy
import subprocess
import sys
import tempfile
import time
import unittest
import urllib.request
from unittest import mock

from scripts import test_install_linux_service as helpers
import comitia_install as local
import install_linux_service as installer


class CredentialsTest(helpers.Base):
    setUpClass = classmethod(helpers.RealBinaryTest.setUpClass.__func__)
    tearDownClass = classmethod(helpers.RealBinaryTest.tearDownClass.__func__)

    def setUp(self):
        super().setUp()
        self.port = helpers.free_port()
        self.reinstall()
        self.run = local.runner(self.root/'bin/comitia', self.root/'data/comitia.db')
        self.context = json.loads((self.root/'local-context.json').read_text())

    def reinstall(self, **kwargs):
        return installer.install(self.root, self.home, port=self.port, binary=self.binary,
                                 service=None, check_port=False, **kwargs)

    def credential(self, name):
        return self.root/'credentials'/f'{name}.json'

    def expire(self, name, file=False):
        path = self.credential(name)
        document = json.loads(path.read_text())
        expiry = (datetime.datetime.now(datetime.timezone.utc)-datetime.timedelta(days=1)).isoformat()
        with sqlite3.connect(self.root/'data/comitia.db') as db:
            db.execute('UPDATE sessions SET expires_at=? WHERE id=?',
                       (expiry, document['credential']['Session']['ID']))
        if file:
            document['credential']['Session']['ExpiresAt'] = expiry
            path.write_text(json.dumps(document))

    def snapshot(self):
        return {p.name: p.read_bytes() for p in (self.root/'credentials').glob('*.json')}

    def test_repair_rejects_expired_owner_and_preserves_data(self):
        self.expire('owner', file=True)
        before = self.snapshot()
        with self.assertRaisesRegex(RuntimeError, 'owner.*expired|owner.*unusable'):
            self.reinstall()
        self.assertEqual(self.snapshot(), before)
        with sqlite3.connect(self.root/'data/comitia.db') as db:
            self.assertEqual(db.execute('SELECT count(*) FROM contexts').fetchone()[0], 1)

    def test_repair_checks_database_expiry_of_existing_harness_even_when_omitted(self):
        self.expire('codex')
        before = self.snapshot()
        with self.assertRaisesRegex(RuntimeError, 'codex.*unusable'):
            self.reinstall(names=[])
        self.assertEqual(self.snapshot(), before)

    def test_add_harness_rejects_existing_expired_credential(self):
        self.expire('codex', file=True)
        with self.assertRaisesRegex(RuntimeError, 'codex.*expired|codex.*unusable'):
            local.add_harness('codex', local.harnesses(self.root)['codex'], self.root, self.home, self.run)

    def test_repair_checks_revocation_and_grant_ancestry(self):
        document = json.loads(self.credential('codex').read_text())
        self.run('grant-revoke', '--grant-id', document['grants'][0]['ID'], token=local.token(self.credential('owner')))
        with self.assertRaisesRegex(RuntimeError, 'codex.*unusable'):
            self.reinstall()

    def start_server(self):
        env = {k: v for k, v in local.os.environ.items() if not k.startswith('COMITIA_')}
        server = subprocess.Popen([str(self.binary), '--db', str(self.root/'data/comitia.db'), 'serve',
                                   '--listen', f'127.0.0.1:{self.port}'], env=env,
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.addCleanup(server.wait)
        self.addCleanup(server.terminate)
        for _ in range(100):
            try:
                urllib.request.urlopen(self.context['server_url']+'/ui/', timeout=1).close()
                return
            except OSError:
                time.sleep(0.02)
        self.fail('test server did not start')

    def test_rotation_survives_old_owner_expiry_and_preserves_permissions(self):
        self.start_server()
        local.add_harness('reader', {'instructions': str(self.home/'reader.md'),
                          'mcp': {'file': str(self.home/'reader.json'), 'format': 'json'}},
                          self.root, self.home, self.run, 'read')
        before = self.snapshot()
        old = json.loads(before['owner.json'])
        # An independent session initially lacks context access. All grants must
        # be issued by the replacement, never by the expiring owner.
        self.expire('claude')
        archive = local.rotate_credentials(self.root, self.run)
        self.assertEqual(helpers.mode(archive), 0o700)
        for name, content in before.items():
            self.assertEqual((archive/name).read_bytes(), content)
            self.assertEqual(helpers.mode(archive/name), 0o600)
        new = json.loads(self.credential('owner').read_text())
        self.assertFalse(new['credential']['Session'].get('ParentID'))
        self.assertEqual(new['credential']['Session']['PrincipalID'], old['credential']['Session']['PrincipalID'])
        self.assertNotEqual(new['credential']['Token'], old['credential']['Token'])
        with sqlite3.connect(self.root/'data/comitia.db') as db:
            db.execute('UPDATE sessions SET expires_at=? WHERE id=?',
                       ('2000-01-01T00:00:00Z', old['credential']['Session']['ID']))
        for name in ('owner', 'codex', 'claude', 'reader'):
            path = self.credential(name)
            self.assertEqual(helpers.mode(path), 0o600)
            self.run('participants', '--context-id', self.context['context_id'], token=local.token(path))
            self.assertNotEqual(path.read_bytes(), before[path.name])
        reader = json.loads(self.credential('reader').read_text())
        self.assertEqual(reader['grants'][0]['Permissions'], 1)
        self.assertEqual(reader['credential']['Session']['ParentID'], new['credential']['Session']['ID'])
        self.reinstall()

    def test_rotation_can_use_another_active_admin_and_revoke_old_sessions(self):
        self.start_server()
        old = self.snapshot()
        alternate = self.root/'alternate.json'
        local.private_json(alternate, local.mint_owner(self.context['server_url'], json.loads(old['owner.json'])))
        self.expire('owner')
        with contextlib.redirect_stdout(io.StringIO()):
            installer.main(['rotate-credentials', '--owner-credential', str(alternate), '--revoke-old'])
        for name in ('owner', 'claude', 'codex'):
            self.run('participants', '--context-id', self.context['context_id'], token=local.token(self.credential(name)))
            with self.assertRaisesRegex(RuntimeError, 'unauthenticated'):
                self.run('context-list', token=json.loads(old[name+'.json'])['credential']['Token'])

    def test_rotation_supports_old_macos_service_url_location(self):
        self.start_server()
        local.write_text(self.root/'service.json', json.dumps({'server_url': self.context['server_url']}))
        context = dict(self.context)
        context.pop('server_url')
        local.write_text(self.root/'local-context.json', json.dumps(context))
        local.rotate_credentials(self.root, self.run)
        self.run('participants', '--context-id', context['context_id'], token=local.token(self.credential('owner')))

    def test_macos_rotation_command_replaces_viewer_without_service_mutation(self):
        self.root = self.home/'Library/Application Support/Comitia'
        self.reinstall()
        self.run = local.runner(self.root/'bin/comitia', self.root/'data/comitia.db')
        self.context = json.loads((self.root/'local-context.json').read_text())
        ttl = local.child_ttl(self.credential('owner'))
        viewer = self.run('delegate', '--name', 'viewer', '--ttl', ttl, '--grant-ttl', ttl,
                          '--context-id', self.context['context_id'], '--permissions', 'read',
                          token=local.token(self.credential('owner')))
        local.private_json(self.credential('viewer'), viewer)
        self.start_server()
        self.expire('viewer')
        with mock.patch.object(sys, 'platform', 'darwin'), \
                mock.patch.object(sys, 'argv', ['install_macos_service.py', '--rotate-credentials']), \
                contextlib.redirect_stdout(io.StringIO()), self.assertRaises(SystemExit) as exit:
            runpy.run_path(str(helpers.SCRIPTS/'install_macos_service.py'), run_name='__main__')
        self.assertEqual(exit.exception.code, 0)
        self.run('participants', '--context-id', self.context['context_id'], token=local.token(self.credential('viewer')))

    def test_interrupted_publishing_can_be_rotated_again(self):
        self.start_server()
        old = self.snapshot()
        replace = local.os.replace
        def interrupt(source, destination):
            if str(source).endswith('/claude.json'):
                raise OSError('interrupted publish')
            return replace(source, destination)
        with mock.patch.object(local.os, 'replace', side_effect=interrupt), self.assertRaisesRegex(OSError, 'interrupted'):
            local.rotate_credentials(self.root, self.run)
        self.assertNotEqual(self.credential('owner').read_bytes(), old['owner.json'])
        local.rotate_credentials(self.root, self.run)
        self.reinstall()

    def test_failed_rotation_keeps_original_files(self):
        self.start_server()
        before = self.snapshot()
        def fail(*args, **kwargs):
            if args[0] == 'delegate':
                raise RuntimeError('injected delegation failure')
            return self.run(*args, **kwargs)
        with self.assertRaisesRegex(RuntimeError, 'injected'):
            local.rotate_credentials(self.root, fail)
        self.assertEqual(self.snapshot(), before)
        with sqlite3.connect(self.root/'data/comitia.db') as db:
            self.assertEqual(db.execute('SELECT count(*) FROM sessions WHERE revoked_at IS NULL').fetchone()[0], 3)

    def test_all_admins_expired_rotation_fails_actionably(self):
        self.expire('owner')
        before = self.snapshot()
        with self.assertRaisesRegex(RuntimeError, 'all.*admin|every.*admin'):
            local.rotate_credentials(self.root, self.run)
        self.assertEqual(self.snapshot(), before)


class BuildProvenanceTest(unittest.TestCase):
    def test_source_build_stamps_revision_and_dirty_state(self):
        for status, dirty in (('', 'false'), (' M scripts/comitia_install.py\n', 'true'),
                              ('?? new-file\n', 'true')):
            with self.subTest(status=status), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                (root/'bin').mkdir()
                commands = []
                def run(command, **kwargs):
                    commands.append(command)
                    if command[:2] == ['git', 'rev-parse']:
                        return subprocess.CompletedProcess(command, 0, 'a'*40+'\n', '')
                    if command[:2] == ['git', 'status']:
                        return subprocess.CompletedProcess(command, 0, status, '')
                    if command[:2] == ['go', 'build']:
                        Path(command[command.index('-o')+1]).write_bytes(b'binary')
                        return subprocess.CompletedProcess(command, 0)
                    self.fail(command)
                with mock.patch.object(local.subprocess, 'run', side_effect=run):
                    local.install_binary(root)
                build = next(command for command in commands if command[0] == 'go')
                self.assertIn('-ldflags', build)
                flags = build[build.index('-ldflags')+1]
                self.assertIn('github.com/krimvp/comitia.Revision='+'a'*40, flags)
                self.assertIn('github.com/krimvp/comitia.Dirty='+dirty, flags)

    def test_git_unavailable_build_stamps_unknown(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root/'bin').mkdir()
            commands = []
            def run(command, **kwargs):
                if command[0] == 'git':
                    raise FileNotFoundError('git unavailable')
                commands.append(command)
                Path(command[command.index('-o')+1]).write_bytes(b'binary')
                return subprocess.CompletedProcess(command, 0)
            with mock.patch.object(local.subprocess, 'run', side_effect=run):
                local.install_binary(root)
            self.assertIn('-ldflags', commands[0])
            self.assertIn('github.com/krimvp/comitia.Revision=unknown', commands[0][commands[0].index('-ldflags')+1])


if __name__ == '__main__':
    unittest.main()
