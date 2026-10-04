"""Installer and wrapper tests against a temporary HOME and a fake Comitia binary."""
import json
import contextlib
import datetime
import io
import os
from pathlib import Path
import runpy
import shutil
import socket
import stat
import subprocess
import sys
import tempfile
import textwrap
import time
import tomllib
import unittest
from unittest import mock
import urllib.request

SCRIPTS = Path(__file__).resolve().parent
sys.path.insert(0, str(SCRIPTS))
import comitia_install as local  # noqa: E402
import install_linux_service as installer  # noqa: E402

# Records each call and answers the administrative commands the installer uses,
# enforcing like the real service that a child session ends before its parent.
FAKE = textwrap.dedent(f"""\
    #!{sys.executable}
    import datetime, json, os, pathlib, re, sys
    args = sys.argv[1:]
    log = pathlib.Path(os.environ['FAKE_LOG'])
    with log.open('a') as stream:
        stream.write(json.dumps({{'args': args, 'db': os.environ.get('COMITIA_DB'),
            'server': os.environ.get('COMITIA_SERVER_URL'), 'credential': os.environ.get('COMITIA_CREDENTIAL'),
            'leaked': os.environ.get('COMITIA_LEAK')}}) + '\\n')
    command = args[0]
    if command == os.environ.get('FAKE_FAIL'):
        sys.exit('injected failure')
    def flag(name):
        return args[args.index(name) + 1]
    def ttl(name):
        value, unit = re.fullmatch(r'(\\d+)([hs])', flag(name)).groups()
        return datetime.timedelta(**{{'hours' if unit == 'h' else 'seconds': int(value)}})
    now = datetime.datetime.now(datetime.timezone.utc)
    db = pathlib.Path(os.environ['COMITIA_DB'])
    if command == 'bootstrap':
        assert os.environ['COMITIA_BOOTSTRAP_CREDENTIAL'] and not db.exists()
        expires = now + ttl('--ttl')
        db.write_text(json.dumps({{'owner_expires': expires.isoformat()}}))
        out = {{'principal': {{'ID': 'p-owner'}}, 'credential': {{'Token': 'owner-token',
               'Session': {{'ID': 's-owner', 'ExpiresAt': expires.isoformat().replace('+00:00', '123Z')}}}}}}
    elif command in ('workspace-create', 'context-create', 'delegate') and os.environ.get('COMITIA_CREDENTIAL') != 'owner-token':
        sys.exit('forbidden')
    elif command == 'workspace-create':
        out = {{'ID': 'w1'}}
    elif command == 'context-create':
        out = {{'ID': 'c1'}}
    elif command == 'delegate':
        owner = datetime.datetime.fromisoformat(json.loads(db.read_text())['owner_expires'])
        if now + ttl('--ttl') > owner or ttl('--grant-ttl') > ttl('--ttl'):
            sys.exit('{{"error":"forbidden"}}')
        name = flag('--name')
        out = {{'principal': {{'ID': 'p-' + name}}, 'credential': {{'Token': 'tok-' + name,
               'Session': {{'ID': 's-' + name, 'ExpiresAt': (now + ttl('--ttl')).isoformat()}}}}, 'grants': []}}
    else:
        out = {{'args': args}}
    print(json.dumps(out))
""")

def mode(path):
    return stat.S_IMODE(path.stat().st_mode)

class Base(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.home = Path(self.tmp.name)/'home'
        self.root = self.home/'.local/share/comitia'
        self.fake = Path(self.tmp.name)/'fake-comitia'
        self.fake.write_text(FAKE)
        self.fake.chmod(0o755)
        self.log = Path(self.tmp.name)/'calls.jsonl'
        patch = {'FAKE_LOG': str(self.log), 'HOME': str(self.home), 'COMITIA_LEAK': 'x', 'COMITIA_CREDENTIAL': 'inherited'}
        self.saved = {k: os.environ.get(k) for k in patch}
        os.environ.update(patch)
        self.addCleanup(self.restore)
        (self.home/'.codex').mkdir(parents=True)
        (self.home/'.claude').mkdir()

    def restore(self):
        for key, value in self.saved.items():
            if value is None:
                os.environ.pop(key, None)
            else:
                os.environ[key] = value

    def calls(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()] if self.log.exists() else []

    def install(self, **kwargs):
        self.services = []
        kwargs.setdefault('service', lambda *command: self.services.append(command))
        kwargs.setdefault('check_port', False)
        return installer.install(self.root, self.home, binary=self.fake, **kwargs)

class InstallTest(Base):
    def test_fresh_install_reproduces_the_local_setup(self):
        context = self.install()
        self.assertEqual(context, {'workspace_id': 'w1', 'context_id': 'c1', 'server_url': 'http://127.0.0.1:8390'})
        for directory in ('', 'bin', 'data', 'credentials', 'backups'):
            self.assertEqual(mode(self.root/directory), 0o700, directory)
        for name in ('owner', 'codex', 'claude'):
            self.assertEqual(mode(self.root/f'credentials/{name}.json'), 0o600)
        self.assertEqual(local.token(self.root/'credentials/codex.json'), 'tok-codex')
        self.assertEqual(mode(self.root/'data/comitia.db'), 0o600)
        self.assertEqual(mode(self.root/'bin/comitia-client'), 0o700)
        self.assertEqual(json.loads((self.root/'local-context.json').read_text()), context)
        calls = self.calls()
        self.assertEqual([c['args'][0] for c in calls], ['bootstrap', 'workspace-create', 'context-create', 'delegate', 'delegate'])
        self.assertTrue(all(c['leaked'] is None and c['server'] is None for c in calls))
        self.assertEqual(calls[0]['db'], str(self.root/'data/comitia.db.init'))
        self.assertTrue(all(c['db'] == str(self.root/'data/comitia.db') for c in calls[1:]))
        self.assertFalse((self.root/'data/comitia.db.init').exists())
        delegate = calls[3]['args']
        self.assertEqual(delegate[delegate.index('--context-id')+1], 'c1')
        ttl = int(delegate[delegate.index('--ttl')+1].rstrip('s'))
        self.assertTrue(8759*3600 < ttl <= 8760*3600, ttl)
        self.assertEqual(delegate[delegate.index('--grant-ttl')+1], f'{ttl}s')

        unit = (self.home/'.config/systemd/user/comitia.service').read_text()
        self.assertIn(f'ExecStart={self.root}/bin/comitia --db {self.root}/data/comitia.db serve --listen 127.0.0.1:8390', unit)
        self.assertIn('UnsetEnvironment=COMITIA_SERVER_URL COMITIA_CREDENTIAL COMITIA_BOOTSTRAP_CREDENTIAL', unit)
        self.assertEqual(self.services, [('daemon-reload',), ('enable', 'comitia.service'), ('restart', 'comitia.service')])
        self.assertIn('Shared context: c1', (self.root/'INSTALLATION.txt').read_text())

        codex = (self.home/'.codex/config.toml').read_text()
        self.assertIn('[mcp_servers.comitia]\n'
                      f'command = "{self.root}/bin/comitia-client"\n'
                      'args = ["codex", "mcp"]\n'
                      'default_tools_approval_mode = "approve"\n', codex)
        claude = json.loads((self.home/'.claude.json').read_text())
        self.assertEqual(claude['mcpServers']['comitia'],
                         {'type': 'stdio', 'command': str(self.root/'bin/comitia-client'), 'args': ['claude', 'mcp']})
        settings = json.loads((self.home/'.claude/settings.json').read_text())
        self.assertEqual(settings['permissions']['allow'], ['mcp__comitia__*'])

        agents = (self.home/'.codex/AGENTS.md').read_text()
        self.assertTrue(agents.startswith('<!-- comitia-local:start v4 -->\n'))
        self.assertIn('Your configured identity is `codex`. The shared context ID is `c1` at `http://127.0.0.1:8390`.', agents)
        self.assertIn('Several Codex sessions share the `codex` credential', agents)
        self.assertIn(str(self.root/'local-context.json'), agents)
        self.assertNotIn('$', agents)
        self.assertIn('Several Claude sessions', (self.home/'.claude/CLAUDE.md').read_text())

    def test_reinstall_keeps_credentials_and_unrelated_configuration(self):
        (self.home/'.codex/config.toml').write_text('model = "x"\n\n[mcp_servers.comitia]\ncommand = "old"\n'
                                                     'startup_timeout_sec = 30\n\n[mcp_servers.other]\ncommand = "o"\n')
        (self.home/'.claude/settings.json').write_text(json.dumps({'permissions': {'allow': ['Bash(ls)']}, 'theme': 'dark'}))
        (self.home/'.claude.json').write_text(json.dumps({'mcpServers': {'comitia': {'env': {'A': '1'}}, 'x': {}}, 'n': 1}))
        (self.home/'.codex/AGENTS.md').write_text('<!-- comitia-local:start -->\nold text\n<!-- comitia-local:end -->\n\n# Mine\n')
        (self.home/'.claude/CLAUDE.md').write_text('# My rules\n')
        self.install()
        owner = (self.root/'credentials/owner.json').read_text()
        first = len(self.calls())
        self.install(port=8391)
        self.assertTrue(all(c['args'][0] in ('participants', 'context-list') for c in self.calls()[first:]),
                        'reinstall must authenticate without bootstrapping or minting again')
        self.assertEqual((self.root/'credentials/owner.json').read_text(), owner)
        self.assertEqual(json.loads((self.root/'local-context.json').read_text())['server_url'], 'http://127.0.0.1:8391')

        codex = (self.home/'.codex/config.toml').read_text()
        self.assertTrue(codex.startswith('model = "x"\n\n[mcp_servers.comitia]\n'))
        self.assertIn('startup_timeout_sec = 30\n\n[mcp_servers.other]\ncommand = "o"\n', codex)
        self.assertEqual(codex.count('[mcp_servers.comitia]'), 1)
        self.assertNotIn('"old"', codex)
        settings = json.loads((self.home/'.claude/settings.json').read_text())
        self.assertEqual(settings, {'permissions': {'allow': ['Bash(ls)', 'mcp__comitia__*']}, 'theme': 'dark'})
        claude = json.loads((self.home/'.claude.json').read_text())
        self.assertEqual(claude['mcpServers']['comitia']['env'], {'A': '1'})
        self.assertEqual((claude['n'], claude['mcpServers']['x']), (1, {}))
        agents = (self.home/'.codex/AGENTS.md').read_text()
        self.assertEqual(agents.count('comitia-local:start'), 1)
        self.assertNotIn('old text', agents)
        self.assertTrue(agents.endswith('<!-- comitia-local:end -->\n\n# Mine\n'))
        self.assertIn('127.0.0.1:8391', agents)
        self.assertTrue((self.home/'.claude/CLAUDE.md').read_text().endswith('<!-- comitia-local:end -->\n\n# My rules\n'))
        backups = sorted(p.name for p in (self.root/'backups').iterdir())
        self.assertTrue(any(name.startswith('codex_config.toml.') for name in backups), backups)
        self.assertTrue(any(name.startswith('claude_settings.json.') for name in backups), backups)

    def test_install_without_service_never_calls_systemctl(self):
        self.install(service=None)
        self.assertTrue((self.home/'.config/systemd/user/comitia.service').exists())

    def test_install_detects_only_present_harnesses(self):
        (self.home/'.claude').rmdir()
        self.install()
        self.assertTrue((self.root/'credentials/codex.json').exists())
        self.assertFalse((self.root/'credentials/claude.json').exists())
        self.assertFalse((self.home/'.claude').exists())
        with self.assertRaisesRegex(RuntimeError, 'Unknown harness'):
            self.install(names=['pi'])

class AddHarnessTest(Base):
    def add(self, *argv):
        args = installer.parser().parse_args(['add-harness', *argv])
        spec = installer.harness_spec(args, local.harnesses(self.root))
        run = local.runner(self.root/'bin/comitia', self.root/'data/comitia.db')
        local.add_harness(args.name, spec, self.root, self.home, run, args.permissions)
        return spec

    def test_third_harness_is_one_command_and_survives_reinstall(self):
        self.install()
        pi = self.home/'.pi'
        self.add('pi', '--display', 'Pi', '--instructions', str(pi/'AGENTS.md'), '--mcp-config', str(pi/'mcp.json'),
                 '--approve-entry', 'autoApprove=["*"]', '--permissions', 'read,write')
        self.assertEqual(local.token(self.root/'credentials/pi.json'), 'tok-pi')
        delegate = self.calls()[-1]['args']
        self.assertEqual(delegate[delegate.index('--permissions')+1], 'read,write')
        entry = json.loads((pi/'mcp.json').read_text())['mcpServers']['comitia']
        self.assertEqual(entry['args'], ['pi', 'mcp'])
        self.assertEqual(entry['autoApprove'], ['*'])
        self.assertIn('Several Pi sessions share the `pi` credential', (pi/'AGENTS.md').read_text())
        registry = json.loads((self.root/'harnesses.json').read_text())
        self.assertEqual(sorted(registry), ['claude', 'codex', 'pi'])

        (pi/'AGENTS.md').write_text('# Pi rules\n')
        self.install(port=8399)
        self.assertIn('127.0.0.1:8399', (pi/'AGENTS.md').read_text())
        self.assertEqual([c['args'][0] for c in self.calls()].count('delegate'), 3)

    def test_toml_and_list_approvals_for_new_harnesses(self):
        self.install()
        droid = self.home/'.droid'
        self.add('droid', '--instructions', str(droid/'AGENTS.md'), '--mcp-config', str(droid/'config.toml'),
                 '--approve-list', str(droid/'settings.json'), 'tools.allowed', 'comitia/*')
        self.assertIn('[mcp_servers.comitia]\n', (droid/'config.toml').read_text())
        self.assertEqual(json.loads((droid/'settings.json').read_text()), {'tools': {'allowed': ['comitia/*']}})
        self.assertIn('Several droid sessions', (droid/'AGENTS.md').read_text())

    def test_invalid_harnesses_are_rejected_before_minting(self):
        self.install()
        before = len(self.calls())
        for argv, message in ((['owner', '--instructions', 'a', '--mcp-config', 'b.json'], 'Invalid harness name'),
                              (['sessions', '--instructions', 'a', '--mcp-config', 'b.json'], 'Invalid harness name'),
                              (['Bad/Name', '--instructions', 'a', '--mcp-config', 'b.json'], 'Invalid harness name'),
                              (['pi'], 'needs an instruction file'),
                              (['pi', '--instructions', 'a', '--mcp-config', 'b.json', '--approve-entry', '=1'], 'Incomplete')):
            with self.subTest(argv=argv), self.assertRaisesRegex(ValueError, message):
                self.add(*argv)
        self.assertEqual(len(self.calls()), before)
        self.assertFalse((self.root/'credentials/pi.json').exists())

    def test_known_harness_without_approval(self):
        self.install(names=[])
        self.add('codex', '--no-approval')
        self.assertNotIn('default_tools_approval_mode', (self.home/'.codex/config.toml').read_text())

class LifetimeTest(Base):
    def owner_expires_in(self, delta):
        path = self.root/'credentials/owner.json'
        document = json.loads(path.read_text())
        expires = datetime.datetime.now(datetime.timezone.utc)+delta
        document['credential']['Session']['ExpiresAt'] = expires.isoformat()
        path.chmod(0o600)
        path.write_text(json.dumps(document))
        (self.root/'data/comitia.db').write_text(json.dumps({'owner_expires': expires.isoformat()}))

    def test_children_are_bounded_by_the_remaining_owner_lifetime(self):
        self.install(names=[])
        self.owner_expires_in(datetime.timedelta(days=100))
        run = local.runner(self.root/'bin/comitia', self.root/'data/comitia.db')
        local.add_harness('codex', local.harnesses(self.root)['codex'], self.root, self.home, run)
        delegate = self.calls()[-1]['args']
        ttl = int(delegate[delegate.index('--ttl')+1].rstrip('s'))
        self.assertTrue(100*86400-3700 < ttl <= 100*86400-3600, ttl)

    def test_nearly_expired_owner_is_reported(self):
        self.install(names=[])
        self.owner_expires_in(datetime.timedelta(minutes=30))
        run = local.runner(self.root/'bin/comitia', self.root/'data/comitia.db')
        with self.assertRaisesRegex(RuntimeError, 'renew it'):
            local.add_harness('codex', local.harnesses(self.root)['codex'], self.root, self.home, run)
        self.assertFalse((self.root/'credentials/codex.json').exists())

class ResumeTest(Base):
    def fail(self, command):
        os.environ['FAKE_FAIL'] = command
        self.addCleanup(os.environ.pop, 'FAKE_FAIL', None)
        with self.assertRaisesRegex(RuntimeError, f'{command} failed'):
            self.install()
        os.environ.pop('FAKE_FAIL')

    def test_rerun_resumes_after_each_failed_step(self):
        for step in ('bootstrap', 'workspace-create', 'context-create', 'delegate'):
            with self.subTest(step=step):
                shutil.rmtree(self.root, ignore_errors=True)
                self.log.unlink(missing_ok=True)
                self.fail(step)
                context = self.install()
                self.assertEqual((context['workspace_id'], context['context_id']), ('w1', 'c1'))
                commands = [c['args'][0] for c in self.calls()]
                self.assertEqual(commands.count('bootstrap'), 1 if step != 'bootstrap' else 2)
                self.assertEqual(commands.count('workspace-create'), 1 if step != 'workspace-create' else 2)
                self.assertEqual(local.token(self.root/'credentials/codex.json'), 'tok-codex')

    def test_rerun_finishes_moving_the_bootstrapped_database(self):
        real = os.replace
        def interrupted(src, dst):
            if str(src).endswith('.init'):
                raise OSError('interrupted')
            return real(src, dst)
        with mock.patch.object(local.os, 'replace', interrupted), self.assertRaises(OSError):
            self.install()
        self.assertTrue((self.root/'credentials/owner.json').exists())
        self.assertFalse((self.root/'data/comitia.db').exists())
        self.install()
        self.assertEqual([c['args'][0] for c in self.calls()].count('bootstrap'), 1)
        self.assertTrue((self.root/'data/comitia.db').exists())

    def test_owner_without_database_is_refused(self):
        self.install(names=[])
        (self.root/'data/comitia.db').unlink()
        with self.assertRaisesRegex(RuntimeError, 'exists without its database'):
            self.install()

class TomlTest(unittest.TestCase):
    entry = {'command': '/x/comitia-client', 'args': ['codex', 'mcp'], 'default_tools_approval_mode': 'approve'}

    def edit(self, text):
        result = local.set_toml_table(text, 'mcp_servers.comitia', self.entry)
        return result, tomllib.loads(result)

    def test_multiline_values_and_header_forms(self):
        cases = {
            'multiline array': '[mcp_servers.comitia]\ncommand = "old"\nargs = [\n  "codex",\n  "mcp",\n]\n'
                               'env = {A = "1"}\n\n[mcp_servers.other]\ncommand = "keep"\n',
            'header comment': '[mcp_servers.comitia] # local server\ncommand = "old"\nargs = ["codex", "mcp"]\n'
                              'env = {A = "1"}\n\n[mcp_servers.other]\ncommand = "keep"\n',
            'spaced header': '[ mcp_servers . comitia ]\nargs = [\n  ["nested"],\n]\nenv = {A = "1"}\n'
                             '[mcp_servers.other]\ncommand = "keep"\n',
            'multiline string': '[mcp_servers.comitia]\ncommand = """\n[not.a.table]\n"""\nenv = {A = "1"}\n'
                                '[mcp_servers.other]\ncommand = "keep"\n',
            'commented header': '# [mcp_servers.comitia]\n[mcp_servers.comitia]\ncommand = \'old\'\nenv = {A = "1"}\n'
                                '[mcp_servers.other]\ncommand = "keep"\n',
        }
        for label, text in cases.items():
            with self.subTest(label):
                result, parsed = self.edit(text)
                self.assertEqual(parsed['mcp_servers']['comitia'], {**self.entry, 'env': {'A': '1'}})
                self.assertEqual(parsed['mcp_servers']['other'], {'command': 'keep'})
                self.assertEqual(self.edit(result)[0], result)

    def test_appends_after_unrelated_tables(self):
        result, parsed = self.edit('model = "x"\n# [mcp_servers.comitia]\n[features]\nshell = true')
        self.assertEqual(parsed, {'model': 'x', 'features': {'shell': True}, 'mcp_servers': {'comitia': self.entry}})

    def test_refuses_invalid_input_and_output(self):
        with self.assertRaisesRegex(ValueError, 'not valid TOML'):
            self.edit('[mcp_servers.comitia\n')
        with self.assertRaisesRegex(ValueError, 'invalid TOML'):
            self.edit('[mcp_servers]\ncomitia = {command = "x"}\n')

    def test_register_leaves_the_file_alone_when_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp)/'config.toml'
            path.write_text('[mcp_servers]\ncomitia = {command = "x"}\n')
            with self.assertRaises(ValueError):
                local.register_mcp({'file': str(path), 'format': 'toml'}, self.entry, tmp, None)
            self.assertEqual(path.read_text(), '[mcp_servers]\ncomitia = {command = "x"}\n')

class MacOSInstallerTest(Base):
    def test_installer_runs_with_mocked_launchd(self):
        real = subprocess.run
        launchd = []
        def run(command, *args, **kwargs):
            if command[0] in ('launchctl', 'plutil'):
                launchd.append(command)
                return subprocess.CompletedProcess(command, 0, '', '')
            return real(command, *args, **kwargs)
        argv = ['install_macos_service.py', '--binary', str(self.fake)]
        with mock.patch.object(sys, 'platform', 'darwin'), mock.patch.object(sys, 'argv', argv), \
                mock.patch.object(subprocess, 'run', run), mock.patch.object(Path, 'home', lambda: self.home), \
                contextlib.redirect_stdout(io.StringIO()):
            runpy.run_path(str(SCRIPTS/'install_macos_service.py'), run_name='__main__')
        root = self.home/'Library/Application Support/Comitia'
        self.assertEqual(json.loads((root/'local-context.json').read_text()),
                         {'workspace_id': 'w1', 'context_id': 'c1', 'server_url': 'http://127.0.0.1:8080'})
        self.assertEqual(local.token(root/'credentials/viewer.json'), 'tok-World viewer')
        import plistlib
        viewer_config = plistlib.loads((self.home/'Library/LaunchAgents/com.comitia.viewer.plist').read_bytes())
        arguments = viewer_config['ProgramArguments']
        self.assertEqual(arguments[arguments.index('--credential-file') + 1], str(root/'credentials/viewer.json'))
        self.assertNotIn(str(root/'credentials/owner.json'), arguments)
        self.assertEqual(mode(root/'credentials/viewer-launch-key'), 0o600)
        self.assertTrue((self.home/'.local/bin/comitia-viewer').is_symlink())

        plist = (self.home/'Library/LaunchAgents/com.comitia.server.plist').read_bytes()
        self.assertIn(str(root/'bin/comitia').encode(), plist)
        self.assertIn(['launchctl', 'bootstrap', f'gui/{os.getuid()}', str(self.home/'Library/LaunchAgents/com.comitia.server.plist')], launchd)

def free_port():
    with socket.socket() as probe:
        probe.bind(('127.0.0.1', 0))
        return probe.getsockname()[1]

@unittest.skipUnless(shutil.which('go'), 'go is not installed')
class RealBinaryTest(Base):
    """Install, add a harness and call through the wrapper with the real binary."""
    @classmethod
    def setUpClass(cls):
        if not shutil.which('go'):
            raise unittest.SkipTest('go unavailable')
        cls.build = tempfile.TemporaryDirectory()
        cls.binary = Path(cls.build.name)/'comitia'
        env = {**os.environ, 'GOTOOLCHAIN': 'go1.24.13', 'GOFLAGS': '-buildvcs=false'}
        result = subprocess.run(['go', 'build', '-o', str(cls.binary), './cmd/comitia'], cwd=SCRIPTS.parent, env=env,
                                capture_output=True, text=True)
        if result.returncode:
            cls.build.cleanup()
            raise RuntimeError(f'go build failed: {result.stderr.strip()}')

    @classmethod
    def tearDownClass(cls):
        cls.build.cleanup()

    def test_install_add_harness_and_wrapper(self):
        port = free_port()
        installer.install(self.root, self.home, port=port, binary=self.binary, service=None, check_port=False)
        installer.install(self.root, self.home, port=port, binary=self.binary, service=None, check_port=False)
        args = installer.parser().parse_args(['add-harness', 'myagent', '--instructions', str(self.home/'.myagent/AGENTS.md'),
                                              '--mcp-config', str(self.home/'.myagent/mcp.json')])
        spec = installer.harness_spec(args, local.harnesses(self.root))
        local.add_harness('myagent', spec, self.root, self.home,
                          local.runner(self.root/'bin/comitia', self.root/'data/comitia.db'))
        context = json.loads((self.root/'local-context.json').read_text())
        env = {k: v for k, v in os.environ.items() if not k.startswith('COMITIA_')}
        server = subprocess.Popen([str(self.root/'bin/comitia'), '--db', str(self.root/'data/comitia.db'), 'serve',
                                   '--listen', f'127.0.0.1:{port}'], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.addCleanup(server.wait)
        self.addCleanup(server.terminate)
        for _ in range(100):
            try:
                urllib.request.urlopen(f'http://127.0.0.1:{port}/ui/', timeout=1)
                break
            except OSError:
                time.sleep(0.1)
        for identity in ('codex', 'claude', 'myagent'):
            with self.subTest(identity=identity):
                result = subprocess.run([sys.executable, str(self.root/'bin/comitia-client'), '--identity', identity,
                                         'context-list'], capture_output=True, text=True, env=env)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual([c['ID'] for c in json.loads(result.stdout)], [context['context_id']])

    def test_delegated_session_identity_grants_and_cleanup(self):
        port = free_port()
        installer.install(self.root, self.home, port=port, binary=self.binary, service=None, check_port=False)
        config = json.loads((self.root/'local-context.json').read_text())
        env = {k: v for k, v in os.environ.items() if not k.startswith('COMITIA_')}
        server = subprocess.Popen([str(self.root/'bin/comitia'), '--db', str(self.root/'data/comitia.db'), 'serve',
                                   '--listen', f'127.0.0.1:{port}'], env=env,
                                  stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        self.addCleanup(server.wait)
        self.addCleanup(server.terminate)
        for _ in range(100):
            try:
                urllib.request.urlopen(config['server_url']+'/ui/', timeout=1).close()
                break
            except OSError:
                time.sleep(0.1)
        def client(*args, input=None):
            return subprocess.run([sys.executable, str(self.root/'bin/comitia-client'), *args], env=env,
                                  input=input, capture_output=True, text=True, timeout=15)
        def ok(*args):
            result = client(*args)
            self.assertEqual(result.returncode, 0, result.stderr)
            return json.loads(result.stdout)
        requests = [dict(jsonrpc='2.0', id=1, method='initialize',
                         params={'protocolVersion': '2025-06-18'}),
                    dict(jsonrpc='2.0', id=2, method='tools/call', params={'name': 'delegate_session',
                         'arguments': {'name': 'task-helper', 'ttl_seconds': 600,
                                       'grants': [{'context_id': config['context_id'],
                                                   'permissions': ['read', 'write'], 'ttl_seconds': 300}]}})]
        result = client('codex', 'mcp', '--sessions-dir', str(self.root/'sessions'),
                        input=''.join(json.dumps(r)+'\n' for r in requests))
        self.assertEqual(result.returncode, 0, result.stderr)
        reply = json.loads(result.stdout.splitlines()[-1])['result']
        self.assertFalse(reply.get('isError'), reply)
        delegated = reply['structuredContent']['result']
        path = Path(delegated['credential_file'])
        self.assertEqual(path.parent, self.root/'sessions')
        self.assertEqual(mode(path), 0o600)
        self.assertNotIn('Token', result.stdout)
        selector = ('--credential-file', str(path))
        participants = ok(*selector, 'participants', '--context-id', config['context_id'])
        helper = next(p for p in participants if p['SessionID'] == delegated['session_id'])
        self.assertEqual(helper['Name'], 'task-helper')
        self.assertNotEqual(helper['SessionID'], json.loads((self.root/'credentials/codex.json').read_text())['credential']['Session']['ID'])
        message = ok(*selector, 'send', '--context-id', config['context_id'], '--dedupe-key', 'hello', '--body', 'hello')
        self.assertEqual(message['SessionID'], delegated['session_id'])
        run = local.runner(self.root/'bin/comitia', self.root/'data/comitia.db')
        owner = local.token(self.root/'credentials/owner.json')
        other = run('context-create', '--workspace-id', config['workspace_id'], '--name', 'private', token=owner)
        for command in ('participants', 'send'):
            args = ['--context-id', other['ID']]
            if command == 'send':
                args += ['--dedupe-key', 'denied', '--body', 'denied']
            denied = client(*selector, command, *args)
            self.assertNotEqual(denied.returncode, 0)
            self.assertIn('forbidden', denied.stderr)
        self.assertEqual(ok('sessions')[0]['status'], 'active')
        ok('codex', 'grant-revoke', '--grant-id', delegated['grant_ids'][0])
        self.assertNotEqual(client(*selector, 'participants', '--context-id', config['context_id']).returncode, 0)
        # Losing a grant does not revoke the session; cleanup must keep it.
        self.assertEqual(ok('sessions', '--prune')[0]['status'], 'active')
        self.assertTrue(path.exists())
        ok('codex', 'revoke', '--session-id', delegated['session_id'])
        self.assertEqual(ok('sessions')[0]['status'], 'revoked')
        self.assertTrue(ok('sessions', '--prune')[0]['pruned'])
        self.assertFalse(path.exists())
        self.assertEqual(ok('sessions'), [])

class BlockTest(unittest.TestCase):
    def test_render_instance_default_and_manual_guidance(self):
        with tempfile.TemporaryDirectory() as root:
            block = local.render_instructions('codex', {'display': 'Codex'},
                                              {'context_id': 'c1', 'server_url': 'http://localhost:8390'}, Path(root))
        self.assertTrue(block.startswith('<!-- comitia-local:start v4 -->\n'))
        self.assertIsNotNone(local.BLOCK.fullmatch(block))
        self.assertIn('When `server_info` lists `default_agent_instance` and the launcher sets `COMITIA_AGENT_INSTANCE`', block)
        self.assertIn('`COMITIA_AGENT_INSTANCE_LABEL`', block)
        self.assertIn('per-call `agent_instance` plumbing and dedupe-key prefixing are unnecessary', block)
        self.assertIn('When `server_info` lists `agent_instances` and your tool schema accepts it, pass your instance', block)
        self.assertIn('Include the instance ID in each `dedupe_key` for compatibility with older servers', block)
        self.assertIn('Several Codex sessions share the `codex` credential', block)
        self.assertIn('`recipient_instances` alongside `recipient_session_ids`', block)
        self.assertIn('When an agent owns a task or child context', block)
        self.assertIn('`delegate_session`', block)
        self.assertIn('`revoke_session`', block)
        self.assertNotIn('${', block)

    def test_place_block(self):
        block = '<!-- comitia-local:start v3 -->\nnew\n<!-- comitia-local:end -->\n'
        self.assertEqual(local.place_block('', block), block)
        self.assertEqual(local.place_block('# A\n', block), block+'\n# A\n')
        old = '<!-- comitia-local:start v2 -->\nold\n<!-- comitia-local:end -->\n'
        self.assertEqual(local.place_block('x\n'+old+'y\n'+old, block), 'x\n'+block+'y\n')

class WrapperTest(Base):
    def setUp(self):
        super().setUp()
        self.install(service=None)
        echo = self.root/'bin/comitia'
        echo.write_text(f'#!{sys.executable}\nimport json, os, sys\n'
                        "print(json.dumps({'args': sys.argv[1:], 'env': {k: v for k, v in os.environ.items() if k.startswith('COMITIA_')}}))\n")
        echo.chmod(0o700)

    def client(self, *args, env=None):
        return subprocess.run([sys.executable, str(self.root/'bin/comitia-client'), *args], capture_output=True, text=True,
                              env={**os.environ, **(env or {})})

    def ok(self, *args, env=None):
        result = self.client(*args, env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_identity_forms(self):
        expected = {'COMITIA_SERVER_URL': 'http://127.0.0.1:8390', 'COMITIA_CREDENTIAL': 'tok-codex'}
        for selector in (['codex'], ['--identity', 'codex'], ['--identity=codex']):
            with self.subTest(selector=selector):
                out = self.ok(*selector, 'replay', '--context-id', 'c1')
                self.assertEqual(out, {'args': ['replay', '--context-id', 'c1'], 'env': expected})
        self.assertEqual(self.ok('claude', 'mcp')['env']['COMITIA_CREDENTIAL'], 'tok-claude')

    def test_instance_variables_pass_through(self):
        out = self.ok('codex', 'mcp', env={'COMITIA_AGENT_INSTANCE': 'codex-repo-task', 'COMITIA_AGENT_INSTANCE_LABEL': 'Task',
                                           'COMITIA_SERVER_URL': 'http://evil', 'COMITIA_DB': 'x.db'})
        self.assertEqual(out['env'], {'COMITIA_SERVER_URL': 'http://127.0.0.1:8390', 'COMITIA_CREDENTIAL': 'tok-codex',
                                      'COMITIA_AGENT_INSTANCE': 'codex-repo-task', 'COMITIA_AGENT_INSTANCE_LABEL': 'Task'})

    def test_credential_file(self):
        path = Path(self.tmp.name)/'delegated.json'
        local.private_json(path, {'credential': {'Token': 'tok-delegated'}, 'server_url': 'http://127.0.0.1:9999'})
        out = self.ok('--credential-file', str(path), 'context-list')
        self.assertEqual(out['env'], {'COMITIA_SERVER_URL': 'http://127.0.0.1:9999', 'COMITIA_CREDENTIAL': 'tok-delegated'})
        self.assertEqual(out['args'], ['context-list'])
        path.chmod(0o644)
        result = self.client('--credential-file', str(path), 'context-list')
        self.assertNotEqual(result.returncode, 0)
        self.assertIn('mode 0600', result.stderr)

    def test_rejections(self):
        for args, message in ((['owner', 'context-list'], 'Invalid identity'),
                              (['../owner', 'x'], 'Invalid identity'),
                              (['--identity', 'sessions', 'mcp'], 'Invalid identity'),
                              (['--identity=sessions', 'mcp'], 'Invalid identity'),
                              (['pi', 'x'], 'installed: claude, codex'),
                              ([], 'Usage'),
                              (['--identity'], 'Usage'),
                              (['--server-url', 'x'], 'Usage')):
            with self.subTest(args=args):
                result = self.client(*args)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stderr)


@unittest.skipUnless(sys.platform.startswith('linux'), 'Linux /proc only')
class StaleMCPClients(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.proc = self.root/'proc'
        self.proc.mkdir()
        self.binary = self.root/'comitia'
        self.binary.write_bytes(b'current')
        self.old = self.root/'comitia (deleted)'
        self.old.write_bytes(b'old')
        # Preserve mtime: replacement must still be detected by inode.
        current = self.binary.stat()
        os.utime(self.old, ns=(current.st_atime_ns, current.st_mtime_ns))

    def process(self, pid, executable, args):
        process = self.proc/str(pid)
        process.mkdir()
        (process/'exe').symlink_to(executable)
        (process/'cmdline').write_bytes(b'\0'.join(arg.encode() for arg in args)+b'\0')

    def test_report_only_old_mcp_processes_without_credentials(self):
        self.process(10, self.old, [str(self.binary), '--server-url', 'http://localhost',
                                  '--credential', 'super-secret-token', 'mcp'])
        self.process(11, self.binary, [str(self.binary), 'mcp'])
        self.process(12, self.old, [str(self.binary), 'serve'])
        self.process(13, self.old, [str(self.binary), '--credential', 'mcp', 'serve'])
        self.process(14, self.old, [str(self.binary), '--db=local.db', 'mcp'])
        unrelated = self.root/'other'
        unrelated.write_bytes(b'old')
        self.process(15, unrelated, [str(self.binary), 'mcp'])
        self.assertEqual(local.stale_mcp_clients(self.binary, self.proc), [10, 14])
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            self.assertEqual(local.report_stale_mcp_clients(self.binary, self.proc), [10, 14])
        self.assertIn('PIDs: 10, 14', output.getvalue())
        self.assertIn('Restart', output.getvalue())
        self.assertNotIn('super-secret-token', output.getvalue())
        self.assertNotIn('http://localhost', output.getvalue())

    def test_missing_inaccessible_and_exited_processes_are_best_effort(self):
        self.assertEqual(local.stale_mcp_clients(self.binary, self.root/'absent'), [])
        self.assertEqual(local.stale_mcp_clients(self.root/'absent', self.proc), [])
        self.process(20, self.root/'gone', [str(self.binary), 'mcp'])
        self.process(21, self.old, [str(self.binary), 'mcp'])
        with mock.patch.object(local.os, 'readlink', side_effect=PermissionError):
            self.assertEqual(local.stale_mcp_clients(self.binary, self.proc), [])
        (self.proc/'21/cmdline').unlink()
        self.assertEqual(local.stale_mcp_clients(self.binary, self.proc), [])

    def test_other_platforms_do_not_scan_proc(self):
        with mock.patch.object(local.sys, 'platform', 'darwin'):
            self.assertEqual(local.stale_mcp_clients(self.binary, self.proc), [])

    def test_binary_upgrade_reports_stale_clients(self):
        root = self.root/'install'
        (root/'bin').mkdir(parents=True)
        binary = root/'bin/comitia'
        binary.write_bytes(b'old')
        old = root/'bin/comitia (deleted)'
        binary.rename(old)
        binary.write_bytes(b'current')
        self.process(30, old, [str(binary), 'mcp'])
        source = self.root/'new-comitia'
        source.write_bytes(b'upgrade')
        report = local.report_stale_mcp_clients
        output = io.StringIO()
        with contextlib.redirect_stdout(output), mock.patch.object(local, 'report_stale_mcp_clients',
                side_effect=lambda installed: report(installed, self.proc)) as called:
            self.assertEqual(local.install_binary(root, source), binary)
        called.assert_called_once_with(binary)
        self.assertEqual(binary.read_bytes(), b'upgrade')
        self.assertIn('PIDs: 30', output.getvalue())

    def test_instruction_feature_guidance(self):
        block = local.render_instructions('codex', {'display': 'Codex'},
                                         {'context_id': 'test-context', 'server_url': 'http://localhost'}, self.root)
        for text in ('server_info', 'agent_instances', 'default_agent_instance', 'instance_claims',
                     'wait_messages', 'replay_filters', 'delegated_sessions', 'tool schema', 'client version', 'session scope'):
            self.assertIn(text, block)

if __name__ == '__main__':
    unittest.main()
