"""Service repair/removal regressions, with temporary homes and fake service managers."""
import contextlib
import io
import json
import os
from pathlib import Path
import plistlib
import runpy
import shlex
import shutil
import socket
import subprocess
import sys
import time
import tomllib
import unittest
import urllib.request
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_install_linux_service as helpers

import install_linux_service as linux
import comitia_install as local

SCRIPTS = helpers.SCRIPTS


class LinuxRepairTest(helpers.Base):
    @contextlib.contextmanager
    def running_manager(self, port):
        proc = Path(self.tmp.name)/'proc'
        process = proc/'123'
        process.mkdir(parents=True, exist_ok=True)
        args = [str(self.root/'bin/comitia'), '--db', str(self.root/'data/comitia.db'),
                'serve', '--listen', f'127.0.0.1:{port}']
        (process/'cmdline').write_bytes(b'\0'.join(os.fsencode(arg) for arg in args)+b'\0')
        (process/'exe').unlink(missing_ok=True)
        (process/'exe').symlink_to(self.root/'bin/comitia')
        real_run = subprocess.run
        def manager(command, **kwargs):
            if command[0] == 'systemctl':
                return subprocess.CompletedProcess(command, 0, stdout='ActiveState=active\nMainPID=123\n')
            return real_run(command, **kwargs)
        with mock.patch.object(linux, 'PROC', proc, create=True), mock.patch('subprocess.run', side_effect=manager):
            yield

    @unittest.skipUnless(shutil.which('systemd-analyze'), 'systemd-analyze unavailable')
    def test_systemd_accepts_home_paths_with_spaces_and_specifiers(self):
        self.home = self.home/'space %Q "quote" \\slash ${HOME}'
        self.root = self.home/'.local/share/comitia'
        self.install(names=[])
        unit = self.home/'.config/systemd/user/comitia.service'
        result = subprocess.run(['systemd-analyze', 'verify', '--man=no', str(unit)],
                                env={**os.environ, 'SYSTEMD_LOG_LEVEL': 'debug'}, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        command = next(line.split('Command Line: ', 1)[1] for line in (result.stdout+result.stderr).splitlines()
                       if 'Command Line: /usr/bin/env -- ' in line)
        # systemd's dump shell-quotes argv before environment expansion.
        argv = [arg.replace('\\$', '$') for arg in shlex.split(command)]
        self.assertEqual(argv, ['/usr/bin/env', '--', str(self.root/'bin/comitia').replace('$', '$$'), '--db',
                                str(self.root/'data/comitia.db').replace('$', '$$'), 'serve', '--listen', '127.0.0.1:8390'])

    def test_bare_reinstall_keeps_the_saved_port(self):
        port = helpers.free_port()
        self.install(port=port)
        real_run = subprocess.run
        def manager(command, **kwargs):
            if command[0] == 'systemctl':
                return subprocess.CompletedProcess(command, 1)
            return real_run(command, **kwargs)
        with mock.patch('subprocess.run', side_effect=manager), contextlib.redirect_stdout(io.StringIO()):
            context = linux.main(['--binary', str(self.fake), '--no-start'])
        self.assertEqual(context['server_url'], f'http://127.0.0.1:{port}')
        self.assertIn(f'127.0.0.1:{port}', (self.home/'.config/systemd/user/comitia.service').read_text())

    def test_changed_port_is_checked_before_rewriting_an_existing_install(self):
        self.install()
        before = {p: p.read_bytes() for p in self.home.rglob('*') if p.is_file()}
        with socket.socket() as occupied:
            occupied.bind(('127.0.0.1', 0))
            port = occupied.getsockname()[1]
            with contextlib.redirect_stdout(io.StringIO()), self.assertRaisesRegex(RuntimeError, f'Port {port} is already occupied'):
                linux.main(['--binary', str(self.fake), '--port', str(port), '--no-start'])
        self.assertEqual({p: p.read_bytes() for p in before}, before)

    def test_unchanged_port_requires_matching_active_service_to_skip_bind(self):
        with socket.socket() as occupied:
            occupied.bind(('127.0.0.1', 0))
            port = occupied.getsockname()[1]
            self.install(port=port)
            with self.running_manager(port):
                self.install(check_port=True)
                executable = Path(self.tmp.name)/'proc/123/exe'
                executable.unlink()
                executable.symlink_to(str(self.root/'bin/comitia')+' (deleted)')
                self.install(check_port=True)  # Atomic binary replacement leaves the old inode running.
                unit = self.home/'.config/systemd/user/comitia.service'
                unit.write_text('[Service]\nExecStart=/usr/bin/other\n')
                with self.assertRaisesRegex(RuntimeError, 'already occupied'):
                    self.install(check_port=True)

    def test_active_service_without_matching_process_evidence_does_not_own_port(self):
        with socket.socket() as occupied:
            occupied.bind(('127.0.0.1', 0))
            port = occupied.getsockname()[1]
            self.install(port=port)
            for evidence in ('cmdline', 'exe'):
                with self.subTest(evidence=evidence), self.running_manager(port):
                    (Path(self.tmp.name)/'proc/123'/evidence).unlink()
                    with self.assertRaisesRegex(RuntimeError, 'already occupied'):
                        self.install(check_port=True)

    def test_interrupted_or_no_start_port_change_checks_the_running_daemon(self):
        for interrupted in (True, False):
            with self.subTest(interrupted=interrupted), socket.socket() as old_daemon, socket.socket() as unrelated:
                old_daemon.bind(('127.0.0.1', 0))
                old_port = old_daemon.getsockname()[1]
                new_port = helpers.free_port()
                while new_port == old_port:
                    new_port = helpers.free_port()
                self.install(port=old_port)
                def failed_reload(*command):
                    if command != ('show-environment',):
                        raise subprocess.CalledProcessError(1, ['systemctl', *command])
                with self.running_manager(old_port):
                    if interrupted:
                        with self.assertRaises(subprocess.CalledProcessError):
                            self.install(port=new_port, service=failed_reload, check_port=True)
                    else:
                        with contextlib.redirect_stdout(io.StringIO()):
                            linux.main(['--binary', str(self.fake), '--port', str(new_port), '--no-start'])
                    self.assertEqual(local.saved_port(self.root), new_port)
                    unrelated.bind(('127.0.0.1', new_port))
                    before = {p: p.read_bytes() for p in self.home.rglob('*') if p.is_file()}
                    with self.assertRaisesRegex(RuntimeError, f'Port {new_port} is already occupied'):
                        self.install(service=None, check_port=True)
                    self.assertEqual({p: p.read_bytes() for p in before}, before)

    def test_uninstall_removes_managed_settings_and_keeps_user_edits_and_data(self):
        (self.home/'.codex/config.toml').write_text('model = "mine"\n[mcp_servers.other]\ncommand = "other"\n')
        (self.home/'.codex/AGENTS.md').write_text('# My rules\n')
        (self.home/'.claude/settings.json').write_text(json.dumps({'permissions': {'allow': ['Bash(ls)']}}))
        self.install()
        codex = self.home/'.codex/config.toml'
        codex.write_text(codex.read_text()+'\n[mcp_servers.comitia.env]\nUSER_SETTING = "keep"\n')
        claude = self.home/'.claude.json'
        document = json.loads(claude.read_text())
        document['mcpServers']['other'] = {'command': 'keep'}
        document['mcpServers']['comitia']['user_setting'] = 'keep'
        claude.write_text(json.dumps(document))
        saved = {p: p.read_bytes() for p in [self.root/'data/comitia.db', *self.root.glob('credentials/*.json')]}
        commands = []
        with mock.patch.object(linux, 'systemctl', side_effect=lambda *c: commands.append(c)), \
             contextlib.redirect_stdout(io.StringIO()):
            linux.main(['uninstall'])
            linux.main(['uninstall'])
        self.assertFalse((self.home/'.config/systemd/user/comitia.service').exists())
        self.assertIn(('disable', '--now', 'comitia.service'), commands)
        self.assertIn(('daemon-reload',), commands)
        self.assertEqual((self.home/'.codex/AGENTS.md').read_text().strip(), '# My rules')
        self.assertNotIn('comitia-local:start', (self.home/'.claude/CLAUDE.md').read_text())
        self.assertIn('model = "mine"', codex.read_text())
        self.assertIn('[mcp_servers.other]', codex.read_text())
        self.assertIn('USER_SETTING = "keep"', codex.read_text())
        self.assertNotIn('comitia-client', codex.read_text())
        remaining = json.loads(claude.read_text())['mcpServers']
        self.assertEqual(remaining, {'other': {'command': 'keep'}, 'comitia': {'user_setting': 'keep'}})
        self.assertEqual(json.loads((self.home/'.claude/settings.json').read_text())['permissions']['allow'], ['Bash(ls)'])
        self.assertEqual({p: p.read_bytes() for p in saved}, saved)

    def no_user_bus(self, command, **kwargs):
        if command[0] == 'systemctl':
            raise subprocess.CalledProcessError(1, command)
        return self.real_run(command, **kwargs)

    def test_missing_user_bus_is_refused_before_any_change(self):
        (self.home/'.claude/CLAUDE.md').write_text('# My rules\n')
        before = {p: p.read_bytes() for p in self.home.rglob('*') if p.is_file()}
        self.real_run = subprocess.run
        with mock.patch('subprocess.run', side_effect=self.no_user_bus), contextlib.redirect_stdout(io.StringIO()), \
             self.assertRaisesRegex(RuntimeError, r'No systemd user session.*--no-start.*bin/comitia --db .* serve'):
            linux.main(['--binary', str(self.fake), '--port', str(helpers.free_port())])
        self.assertEqual({p: p.read_bytes() for p in self.home.rglob('*') if p.is_file() and p.name != '.install.lock'}, before)
        self.assertEqual(self.calls(), [])
        with mock.patch('subprocess.run', side_effect=self.no_user_bus), contextlib.redirect_stdout(io.StringIO()):
            linux.main(['--binary', str(self.fake), '--port', str(helpers.free_port()), '--no-start'])
        self.assertIn('Without systemd: ', (self.root/'INSTALLATION.txt').read_text())

    def test_uninstall_without_user_bus_still_removes_harness_integration(self):
        self.install()
        unit = self.home/'.config/systemd/user/comitia.service'
        self.real_run = subprocess.run
        output = io.StringIO()
        with mock.patch('subprocess.run', side_effect=self.no_user_bus), contextlib.redirect_stdout(output), \
             self.assertRaisesRegex(SystemExit, r'Could not run: systemctl --user disable --now comitia.service'):
            linux.main(['uninstall'])
        self.assertNotIn('comitia', json.loads((self.home/'.claude.json').read_text()).get('mcpServers', {}))
        self.assertNotIn('comitia-client', (self.home/'.codex/config.toml').read_text())
        self.assertNotIn('comitia-local:start', (self.home/'.claude/CLAUDE.md').read_text())
        self.assertIn(f'manual review: {unit}', output.getvalue())
        self.assertTrue(unit.exists(), 'a service that could not be stopped keeps its unit')
        commands = []
        with mock.patch.object(linux, 'systemctl', side_effect=lambda *c: commands.append(c)), \
             contextlib.redirect_stdout(io.StringIO()):
            linux.main(['uninstall'])
        self.assertEqual(commands, [('disable', '--now', 'comitia.service'), ('daemon-reload',)])
        self.assertFalse(unit.exists())

    def test_uninstall_preserves_replaced_entries_and_preexisting_approvals(self):
        settings = self.home/'.claude/settings.json'
        settings.write_text(json.dumps({'permissions': {'allow': ['mcp__comitia__*', 'Bash(ls)']}}))
        self.install()
        self.install()  # Provenance must survive repair.
        path = self.home/'.claude.json'
        document = json.loads(path.read_text())
        document['mcpServers']['comitia']['command'] = 'my-custom-command'
        path.write_text(json.dumps(document))
        unit = self.home/'.config/systemd/user/comitia.service'
        unit.write_text('[Service]\nExecStart=/usr/bin/my-service\n')
        with mock.patch.object(linux, 'systemctl') as service, contextlib.redirect_stdout(io.StringIO()):
            linux.main(['uninstall'])
        service.assert_not_called()
        self.assertEqual(json.loads(path.read_text()), document)
        self.assertEqual(json.loads(settings.read_text())['permissions']['allow'], ['mcp__comitia__*', 'Bash(ls)'])
        self.assertEqual(unit.read_text(), '[Service]\nExecStart=/usr/bin/my-service\n')

    def test_uninstall_quoted_keys_preserves_unrelated_toml_structure(self):
        for quote in ('"', "'"):
            with self.subTest(quote=quote):
                path = self.home/'.codex/config.toml'
                path.write_text('[features]\nshell = true\n')
                self.install(names=['codex'])
                text = path.read_text().replace('command =', f'{quote}command{quote} =').replace(
                    'args =', f'{quote}args{quote} =')
                path.write_text(text)
                expected = tomllib.loads(text)
                del expected['mcp_servers']['comitia']
                linux.uninstall(self.root, self.home, service=None)
                self.assertEqual(tomllib.loads(path.read_text()), expected)
                after = path.read_bytes()
                linux.uninstall(self.root, self.home, service=None)
                self.assertEqual(path.read_bytes(), after)

    def test_uninstall_refuses_a_semantic_change_before_writing_toml(self):
        self.install(names=['codex'])
        path = self.home/'.codex/config.toml'
        original = path.read_text()
        # TOML comments end at LF; splitlines also splits these Unicode characters.
        for separator in ('\u0085', '\u2028'):
            with self.subTest(separator=repr(separator)):
                text = original.replace('args =', f'# comment {separator}[other]\nargs =')
                path.write_text(text)
                self.assertEqual(tomllib.loads(text)['mcp_servers']['comitia']['args'], ['codex', 'mcp'])
                with self.assertRaisesRegex(ValueError, 'would change unrelated TOML'):
                    local.remove_harnesses(self.root, self.home)
                self.assertEqual(path.read_text(), text)

    def test_uninstall_escaped_keys_multiline_args_and_custom_subtables(self):
        self.install(names=['codex'])
        path = self.home/'.codex/config.toml'
        text = path.read_text().replace('command =', '"\\u0063ommand" =').replace(
            'args = ["codex", "mcp"]', '"args" = [\n  "codex", # keep argument\n  "mcp",\n]')
        text += '\n[mcp_servers.comitia.env]\nUSER_SETTING = "keep"\n'
        path.write_text(text)
        expected = tomllib.loads(text)
        expected['mcp_servers']['comitia'] = {'env': {'USER_SETTING': 'keep'}}
        linux.uninstall(self.root, self.home, service=None)
        self.assertEqual(tomllib.loads(path.read_text()), expected)

    def test_uninstall_also_cleans_previous_harness_config_locations(self):
        self.install()
        spec = {'instructions': str(self.home/'moved/AGENTS.md'),
                'mcp': {'file': str(self.home/'moved/mcp.json'), 'format': 'json'}}
        local.add_harness('codex', spec, self.root, self.home,
                          local.runner(self.root/'bin/comitia', self.root/'data/comitia.db'))
        linux.uninstall(self.root, self.home, service=None)
        for path in (self.home/'.codex/AGENTS.md', self.home/'moved/AGENTS.md'):
            self.assertNotIn('comitia-local:start', path.read_text())
        self.assertNotIn('comitia-client', (self.home/'.codex/config.toml').read_text())
        self.assertNotIn('comitia', json.loads((self.home/'moved/mcp.json').read_text())['mcpServers'])


class MacFixture(helpers.Base):
    def setUp(self):
        super().setUp()
        self.root = self.home/'Library/Application Support/Comitia'
        self.loaded = set()
        self.loaded_configs = {}
        self.fail_lint = None
        self.running = True
        self.manager_calls = []
        self.fail_launchctl = False
        self.server_port, self.viewer_port = helpers.free_port(), helpers.free_port()
        while self.viewer_port == self.server_port:
            self.viewer_port = helpers.free_port()

    def install_mac(self):
        self.mac('--port', str(self.server_port), '--viewer-port', str(self.viewer_port))

    def mac(self, *args):
        real_run = subprocess.run
        def manager(command, **kwargs):
            if command[0] in ('launchctl', 'plutil'):
                self.manager_calls.append(command)
                if self.fail_launchctl and command[:2] in (['launchctl', 'disable'], ['launchctl', 'bootout']):
                    raise subprocess.CalledProcessError(1, command)
                status, output = 0, ''
                if command[:2] == ['launchctl', 'print']:
                    label = command[-1].split('/')[-1]
                    status = int(label not in self.loaded)
                    if not status:
                        output = 'state = '+('running' if self.running else 'waiting')+'\narguments = {\n'+''.join(
                            f'\t{arg}\n' for arg in self.loaded_configs[label]['ProgramArguments'])+'}\npid = 123\n'
                elif command[:2] == ['launchctl', 'bootstrap']:
                    label = Path(command[-1]).stem
                    self.loaded.add(label)
                    self.loaded_configs[label] = plistlib.loads(Path(command[-1]).read_bytes())
                elif command[:2] == ['launchctl', 'bootout']:
                    self.loaded.discard(command[-1].split('/')[-1])
                elif command[0] == 'plutil' and Path(command[-1]).stem == self.fail_lint:
                    raise subprocess.CalledProcessError(1, command)
                return subprocess.CompletedProcess(command, status, stdout=output if kwargs.get('text') else output.encode(),
                                                   stderr='' if kwargs.get('text') else b'')
            return real_run(command, **kwargs)
        with mock.patch('sys.platform', 'darwin'), mock.patch('sys.argv', ['installer', '--binary', str(self.fake), *args]), \
             mock.patch('subprocess.run', side_effect=manager), contextlib.redirect_stdout(io.StringIO()):
            runpy.run_path(str(SCRIPTS/'install_macos_service.py'), run_name='__main__')

class MacRepairTest(MacFixture):
    def test_bare_reinstall_keeps_both_saved_ports(self):
        self.install_mac()
        self.mac()
        config = json.loads((self.root/'service.json').read_text())
        self.assertEqual(config, {'server_url': f'http://127.0.0.1:{self.server_port}',
                                  'viewer_url': f'http://127.0.0.1:{self.viewer_port}'})

    def test_changed_ports_are_checked_even_when_launchd_has_loaded_services(self):
        self.install_mac()
        for flag in ('--port', '--viewer-port'):
            with self.subTest(flag=flag), socket.socket() as occupied:
                occupied.bind(('127.0.0.1', 0))
                port = occupied.getsockname()[1]
                before = {p: p.read_bytes() for p in self.home.rglob('*') if p.is_file()}
                calls = len(self.manager_calls)
                with self.assertRaisesRegex(SystemExit, f'Port {port} is already occupied'):
                    self.mac(flag, str(port))
                self.assertEqual({p: p.read_bytes() for p in before}, before)
                self.assertFalse(any(c[1] != 'print' for c in self.manager_calls[calls:]))

    def test_interrupted_port_change_checks_loaded_arguments_not_rewritten_plist(self):
        for flag, label, key in (('--port', 'com.comitia.server', 'server_url'),
                                 ('--viewer-port', 'com.comitia.viewer', 'viewer_url')):
            with self.subTest(flag=flag), socket.socket() as old_daemon, socket.socket() as unrelated:
                self.install_mac()
                old_port = self.server_port if key == 'server_url' else self.viewer_port
                old_daemon.bind(('127.0.0.1', old_port))
                new_port = helpers.free_port()
                while new_port in (self.server_port, self.viewer_port):
                    new_port = helpers.free_port()
                loaded = self.loaded_configs[label]
                self.fail_lint = label
                with self.assertRaisesRegex(SystemExit, 'plutil'):
                    self.mac(flag, str(new_port))
                self.fail_lint = None
                self.assertEqual(local.saved_port(self.root, key), new_port)
                self.assertEqual(self.loaded_configs[label], loaded)
                unrelated.bind(('127.0.0.1', new_port))
                before = {p: p.read_bytes() for p in self.home.rglob('*') if p.is_file()}
                calls = len(self.manager_calls)
                with self.assertRaisesRegex(SystemExit, f'Port {new_port} is already occupied'):
                    self.mac()
                self.assertEqual({p: p.read_bytes() for p in before}, before)
                self.assertFalse(any(c[1] != 'print' for c in self.manager_calls[calls:]))

    def test_matching_running_jobs_allow_repair_of_occupied_saved_ports(self):
        self.install_mac()
        with socket.socket() as server, socket.socket() as viewer:
            server.bind(('127.0.0.1', self.server_port))
            viewer.bind(('127.0.0.1', self.viewer_port))
            self.mac()

    def test_loaded_job_without_running_process_evidence_does_not_own_port(self):
        self.install_mac()
        with socket.socket() as occupied:
            occupied.bind(('127.0.0.1', self.server_port))
            self.running = False
            before = {p: p.read_bytes() for p in self.home.rglob('*') if p.is_file()}
            with self.assertRaisesRegex(SystemExit, 'already occupied'):
                self.mac()
            self.assertEqual({p: p.read_bytes() for p in before}, before)

    def test_uninstall_removes_only_matching_plists_and_links_and_keeps_data(self):
        self.install_mac()
        saved = {p: p.read_bytes() for p in [self.root/'data/comitia.db', self.root/'credentials/viewer-launch-key',
                                              *self.root.glob('credentials/*.json')]}
        custom = self.home/'.local/bin/comitia'
        custom.unlink()
        custom.write_text('my command\n')
        unrelated = self.home/'Library/LaunchAgents/other.plist'
        unrelated.write_bytes(plistlib.dumps({'Label': 'other', 'ProgramArguments': ['keep']}))
        self.mac('--uninstall')
        self.mac('--uninstall')
        self.assertFalse((self.home/'Library/LaunchAgents/com.comitia.server.plist').exists())
        self.assertFalse((self.home/'Library/LaunchAgents/com.comitia.viewer.plist').exists())
        self.assertFalse((self.home/'.local/bin/comitia-local').is_symlink())
        self.assertFalse((self.home/'.local/bin/comitia-viewer').is_symlink())
        self.assertEqual(custom.read_text(), 'my command\n')
        self.assertTrue(unrelated.exists())
        self.assertEqual({p: p.read_bytes() for p in saved}, saved)
        for label in ('com.comitia.server', 'com.comitia.viewer'):
            self.assertTrue(any(c[1:3] == ['disable', f'gui/{os.getuid()}/{label}'] for c in self.manager_calls))

    def test_uninstall_with_failing_launchctl_still_removes_harness_integration(self):
        self.install_mac()
        self.fail_launchctl = True
        with self.assertRaisesRegex(SystemExit, r'Could not run: launchctl disable gui/'):
            self.mac('--uninstall')
        self.assertNotIn('comitia', json.loads((self.home/'.claude.json').read_text()).get('mcpServers', {}))
        self.assertNotIn('comitia-local:start', (self.home/'.claude/CLAUDE.md').read_text())
        self.assertTrue((self.home/'Library/LaunchAgents/com.comitia.server.plist').exists())
        self.fail_launchctl = False
        self.mac('--uninstall')
        self.assertFalse((self.home/'Library/LaunchAgents/com.comitia.server.plist').exists())

    def test_uninstall_removes_viewer_plist_from_before_the_scoped_launcher(self):
        self.install_mac()
        path = self.home/'Library/LaunchAgents/com.comitia.viewer.plist'
        config = plistlib.loads(path.read_bytes())
        config['ProgramArguments'] = config['ProgramArguments'][:-4] + ['--credential-file', str(self.root/'credentials/owner.json')]
        path.write_bytes(plistlib.dumps(config))
        self.mac('--uninstall')
        self.assertFalse(path.exists())

    def test_unrelated_viewer_command_is_refused_before_installing(self):
        link = self.home/'.local/bin/comitia-viewer'
        link.parent.mkdir(parents=True, exist_ok=True)
        link.write_text('my viewer\n')
        with self.assertRaisesRegex(SystemExit, 'unrelated command'):
            self.install_mac()
        self.assertEqual([p.name for p in self.root.iterdir()] if self.root.exists() else [], ['.install.lock'])
        self.assertEqual(link.read_text(), 'my viewer\n')


class MacDaemonTest(MacFixture):
    setUpClass = classmethod(helpers.RealBinaryTest.setUpClass.__func__)
    tearDownClass = classmethod(helpers.RealBinaryTest.tearDownClass.__func__)

    def test_daemon_binds_despite_inherited_client_routing_and_secrets(self):
        self.fake = self.binary
        port, viewer_port = helpers.free_port(), helpers.free_port()
        while viewer_port == port:
            viewer_port = helpers.free_port()
        with mock.patch.dict(os.environ, {'COMITIA_SERVER_URL': 'http://127.0.0.1:1',
                                         'COMITIA_CREDENTIAL': 'client-secret',
                                         'COMITIA_BOOTSTRAP_CREDENTIAL': 'bootstrap-secret'}):
            self.mac('--port', str(port), '--viewer-port', str(viewer_port))
            config = plistlib.loads((self.home/'Library/LaunchAgents/com.comitia.server.plist').read_bytes())
            env = dict(os.environ)
            env.update(config.get('EnvironmentVariables', {}))
            server = subprocess.Popen(config['ProgramArguments'], env=env, stdout=subprocess.DEVNULL,
                                      stderr=subprocess.PIPE, text=True)
            try:
                opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
                for _ in range(100):
                    if server.poll() is not None:
                        self.fail('daemon exited before binding: '+server.stderr.read())
                    try:
                        with opener.open(f'http://127.0.0.1:{port}/ui/', timeout=0.1) as response:
                            self.assertEqual(response.status, 200)
                        break
                    except OSError:
                        time.sleep(0.02)
                else:
                    self.fail('daemon did not bind')
                for key in ('COMITIA_SERVER_URL', 'COMITIA_CREDENTIAL', 'COMITIA_BOOTSTRAP_CREDENTIAL'):
                    self.assertFalse(env.get(key), key)
            finally:
                if server.poll() is None:
                    server.terminate()
                server.wait(timeout=5)
                server.stderr.close()


if __name__ == '__main__':
    unittest.main()
