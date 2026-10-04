import contextlib
import datetime
import io
import http.server
import json
import os
from pathlib import Path
import sys
import subprocess
import tempfile
import threading
import unittest
from unittest import mock
import urllib.error

sys.path.insert(0, str(Path(__file__).resolve().parent))
import comitia_client as client


class SessionsTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        self.directory = self.root/'sessions'
        self.directory.mkdir(mode=0o700)
        (self.root/'local-context.json').write_text(json.dumps({'server_url': 'http://localhost:8390'}))

    def credential(self, name, delta=3600):
        expiry = datetime.datetime.now(datetime.timezone.utc)+datetime.timedelta(seconds=delta)
        document = {'credential': {'Token': 'private-token', 'Session': {'ID': name, 'ExpiresAt': expiry.isoformat()}}}
        path = self.directory/(name+'.json')
        path.write_text(json.dumps(document))
        path.chmod(0o600)
        return path, document

    def test_expired_prune_stays_inside_sessions(self):
        expired, _ = self.credential('expired', -60)
        outside = self.root/'outside.json'
        outside.write_text(expired.read_text())
        rows = client.sessions(['--prune'], self.root)
        self.assertEqual(rows[0]['status'], 'expired')
        self.assertTrue(rows[0]['pruned'])
        self.assertFalse(expired.exists())
        self.assertTrue(outside.exists())
        self.assertNotIn('private-token', json.dumps(rows))

    def test_offline_is_unknown_and_kept(self):
        path, _ = self.credential('offline')
        with mock.patch.object(client.urllib.request, 'build_opener') as opener:
            opener.return_value.open.side_effect = urllib.error.URLError('offline')
            self.assertEqual(client.sessions(['--prune'], self.root)[0]['status'], 'unknown')
        self.assertTrue(path.exists())

    def test_revocation_requires_unauthenticated_response(self):
        path, document = self.credential('session')
        with mock.patch.object(client.urllib.request, 'build_opener') as opener:
            for code, status in ((401, 'revoked'), (403, 'unknown'), (500, 'unknown')):
                opener.return_value.open.side_effect = urllib.error.HTTPError('http://localhost', code, '', {}, None)
                self.assertEqual(client.session_status(document, 'http://localhost'), status)
            opener.return_value.open.side_effect = None
            self.assertEqual(client.session_status(document, 'http://localhost'), 'active')
            request = opener.return_value.open.call_args.args[0]
            self.assertEqual(json.loads(request.data), {'limit': 1})
            self.assertEqual(request.get_header('Authorization'), 'Bearer private-token')

    def test_refuses_symlink_file_and_directory(self):
        path, _ = self.credential('expired', -60)
        outside = self.root/'outside.json'
        path.rename(outside)
        path.symlink_to(outside)
        self.assertEqual(client.sessions(['--prune'], self.root)[0]['status'], 'invalid')
        self.assertTrue(path.is_symlink())
        self.assertTrue(outside.exists())
        path.unlink()
        self.directory.rmdir()
        self.directory.symlink_to(self.root, target_is_directory=True)
        with self.assertRaises(SystemExit):
            client.sessions(['--prune'], self.root)

    def test_refuses_public_file_and_invalid_options(self):
        path, _ = self.credential('public', -60)
        path.chmod(0o644)
        self.assertEqual(client.sessions(['--prune'], self.root)[0]['status'], 'invalid')
        self.assertTrue(path.exists())
        with self.assertRaises(SystemExit):
            client.sessions(['--prune', '../outside'], self.root)

    def test_missing_directory(self):
        self.directory.rmdir()
        self.assertEqual(client.sessions([], self.root), [])

    def test_prune_reports_all_removals_and_keeps_invalid_files(self):
        expired, _ = self.credential('a-expired', -60)
        invalid = []
        for name, content in (('b-empty', ''), ('c-partial', '{"credential":'),
                              ('d-missing', '{}'), ('e-null', 'null'),
                              ('f-wrong-type', '{"credential": []}'),
                              ('g-invalid-expiry', '{"credential":{"Token":"secret","Session":{"ID":"x","ExpiresAt":"bad"}}}')):
            path = self.directory/(name+'.json')
            path.write_text(content)
            path.chmod(0o600)
            invalid.append(path)
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            client.main(['sessions', '--prune'], self.root)
        rows = json.loads(output.getvalue())
        self.assertTrue(rows[0]['pruned'])
        self.assertFalse(expired.exists())
        self.assertEqual([r['status'] for r in rows[1:]], ['invalid']*len(invalid))
        self.assertTrue(all(p.exists() for p in invalid))
        self.assertNotIn('secret', output.getvalue())

    def test_unreadable_file_does_not_hide_prune_report(self):
        expired, _ = self.credential('a-expired', -60)
        unreadable, _ = self.credential('b-unreadable')
        original = os.open
        def open_file(path, *args, **kwargs):
            if path == unreadable.name:
                raise PermissionError('unreadable')
            return original(path, *args, **kwargs)
        with mock.patch.object(client.os, 'open', side_effect=open_file):
            rows = client.sessions(['--prune'], self.root)
        self.assertTrue(rows[0]['pruned'])
        self.assertEqual(rows[1]['status'], 'invalid')
        self.assertFalse(expired.exists())
        self.assertTrue(unreadable.exists())


class SelectedCredentialTest(unittest.TestCase):
    def setUp(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        self.root = Path(temp.name)
        (self.root/'credentials').mkdir()
        self.path = self.root/'credentials/worker.json'
        self.path.write_text(json.dumps({'server_url': 'http://selected-server',
                                        'credential': {'Token': 'selected-secret'}}))
        self.path.chmod(0o600)
        (self.root/'local-context.json').write_text(json.dumps({'server_url': 'http://fallback-server'}))

    def test_rejects_overrides_before_exec(self):
        selectors = [['worker'], ['--identity=worker'], ['--credential-file', str(self.path)]]
        for selector in selectors:
            for name in ('server-url', 'db', 'credential', 'bootstrap-credential'):
                for dash in ('-', '--'):
                    for override in ([dash+name, 'override-secret'], [dash+name+'=override-secret']):
                        with self.subTest(selector=selector, override=override), mock.patch.object(client.os, 'execve') as execute:
                            with self.assertRaises(SystemExit) as error:
                                client.main([*selector, *override, 'context-list'], self.root)
                            execute.assert_not_called()
                            self.assertNotIn('override-secret', str(error.exception))
                            self.assertNotIn('selected-secret', str(error.exception))

    def test_selected_destination_and_credential_override_environment(self):
        with mock.patch.dict(os.environ, {'COMITIA_SERVER_URL': 'http://hostile-server',
                                         'COMITIA_CREDENTIAL': 'hostile-secret',
                                         'COMITIA_DB': 'hostile.db',
                                         'COMITIA_AGENT_INSTANCE': 'worker-instance'}), \
                mock.patch.object(client.os, 'execve') as execute:
            args = ['context-list', '--limit', '1']
            client.main(['worker', *args], self.root)
            binary, argv, env = execute.call_args.args
            self.assertEqual(argv, [binary, *args])
            self.assertEqual(env['COMITIA_SERVER_URL'], 'http://selected-server')
            self.assertEqual(env['COMITIA_CREDENTIAL'], 'selected-secret')
            self.assertEqual(env['COMITIA_AGENT_INSTANCE'], 'worker-instance')
            self.assertNotIn('COMITIA_DB', env)

    def test_real_binary_sends_only_to_selected_server(self):
        (self.root/'bin').mkdir()
        env = {k: v for k, v in os.environ.items() if not k.startswith('COMITIA_')}
        env.update(GOTOOLCHAIN='go1.24.13', GOFLAGS='-buildvcs=false')
        cache, gopath = subprocess.check_output(['go', 'env', 'GOCACHE', 'GOPATH'], env=env, text=True).splitlines()
        env.update(HOME=str(self.root), GOPATH=gopath, GOCACHE=cache,
                   HTTP_PROXY='', HTTPS_PROXY='', ALL_PROXY='', NO_PROXY='127.0.0.1')
        subprocess.run(['go', 'build', '-o', str(self.root/'bin/comitia'), './cmd/comitia'],
                       cwd=Path(__file__).resolve().parent.parent, env=env,
                       check=True, capture_output=True, timeout=120)

        def capture_server():
            requests = []
            class Capture(http.server.BaseHTTPRequestHandler):
                def do_POST(self):
                    requests.append(self.headers.get('Authorization'))
                    self.rfile.read(int(self.headers.get('Content-Length', '0')))
                    self.send_response(200)
                    self.end_headers()
                    self.wfile.write(b'{"contexts":[]}')
                def log_message(self, *args):
                    pass
            server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Capture)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            def stop():
                server.shutdown()
                server.server_close()
                thread.join()
            self.addCleanup(stop)
            return 'http://127.0.0.1:'+str(server.server_port), requests

        selected_url, selected_requests = capture_server()
        override_url, override_requests = capture_server()
        self.path.write_text(json.dumps({'server_url': selected_url,
                                        'credential': {'Token': 'selected-secret'}}))
        env.update(COMITIA_SERVER_URL=override_url, COMITIA_CREDENTIAL='inherited-secret')
        code = """import importlib.util, pathlib, sys
spec = importlib.util.spec_from_file_location('client', sys.argv[1])
client = importlib.util.module_from_spec(spec)
spec.loader.exec_module(client)
client.main(sys.argv[3:], pathlib.Path(sys.argv[2]))
"""
        command = [sys.executable, '-c', code, client.__file__, str(self.root)]
        for selector in (['worker'], ['--identity', 'worker'], ['--credential-file', str(self.path)]):
            for override in (['--server-url', override_url], ['--server-url='+override_url],
                             ['-server-url', override_url], ['-server-url='+override_url]):
                with self.subTest(selector=selector, override=override):
                    result = subprocess.run([*command, *selector, *override, 'context-list'],
                                            env=env, capture_output=True, text=True, timeout=10)
                    self.assertNotEqual(result.returncode, 0)
                    self.assertNotIn('selected-secret', result.stdout+result.stderr)
                    self.assertEqual(override_requests, [])
                    self.assertEqual(selected_requests, [])
        result = subprocess.run([*command, 'worker', 'context-list'], env=env,
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(selected_requests, ['Bearer selected-secret'])
        self.assertEqual(override_requests, [])
