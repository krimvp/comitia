"""Viewer boundary tests use private temp files and loopback fixture servers."""
import http.client
import http.server
import json
import os
import runpy
from unittest import mock
from pathlib import Path
import socket
import subprocess
import sys
import tempfile
import threading
import time
import unittest

ROOT = Path(__file__).resolve().parent.parent

class ViewerTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.key = 'private-launch-key-' + 'x' * 40
        self.credential = self.root/'viewer.json'
        self.credential.write_text(json.dumps({'credential': {'Token': 'ct_scoped-viewer'}}))
        self.credential.chmod(0o600)
        (self.root/'viewer-launch-key').write_text(self.key)
        (self.root/'viewer-launch-key').chmod(0o600)
        self.calls = []
        calls = self.calls
        class Upstream(http.server.BaseHTTPRequestHandler):
            def do_POST(handler):
                body = json.loads(handler.rfile.read(int(handler.headers['Content-Length'])))
                calls.append((handler.path, handler.headers.get('Authorization'), body))
                status = 403 if body.get('context_id') == 'private-child' else 200
                handler.send_response(status)
                handler.send_header('Content-Type', 'application/json')
                handler.end_headers()
                handler.wfile.write(json.dumps({'contexts': [{'ID': 'scoped'}]}).encode())
            def log_message(self, *args):
                pass
        self.upstream = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Upstream)
        self.addCleanup(self.upstream.server_close)
        self.addCleanup(self.upstream.shutdown)
        threading.Thread(target=self.upstream.serve_forever, daemon=True).start()
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            self.port = probe.getsockname()[1]
        self.start_viewer()

    def start_viewer(self):
        self.process = subprocess.Popen([sys.executable, str(ROOT/'tools/world-viewer/serve.py'),
            '--port', str(self.port), '--upstream', f'http://127.0.0.1:{self.upstream.server_port}',
            '--credential-file', str(self.credential)], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
        self.addCleanup(self.stop_viewer)
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                self.fail(self.process.stderr.read().decode())
            try:
                self.request('GET', '/viewer/session')
                return
            except OSError:
                time.sleep(0.02)
        self.fail('viewer did not start')

    def stop_viewer(self):
        if self.process.poll() is None:
            self.process.terminate()
        self.process.wait(timeout=5)
        self.process.stderr.close()

    def request(self, method, path, body=None, headers=None):
        connection = http.client.HTTPConnection('127.0.0.1', self.port, timeout=3)
        try:
            connection.request(method, path, json.dumps(body) if body is not None else None,
                               headers or {})
            response = connection.getresponse()
            return response.status, dict(response.getheaders()), response.read()
        finally:
            connection.close()

    def login(self):
        status, headers, payload = self.request('POST', '/viewer/login', {'key': self.key})
        self.assertEqual(status, 200, payload)
        session = json.loads(payload)['session']
        self.assertNotEqual(session, self.key)
        return {'X-Comitia-Viewer-Session': session}

    def test_native_caller_cannot_use_automatic_credential(self):
        status, _, payload = self.request('POST', '/v1/messages/replay', {'context_id': 'scoped'})
        self.assertEqual(status, 401, payload)
        self.assertEqual(self.calls, [])
        status, _, payload = self.request('GET', '/viewer/session')
        self.assertEqual(json.loads(payload), {'automatic': False})

    def test_launcher_session_reads_with_scoped_server_credential(self):
        headers = self.login()
        status, _, payload = self.request('GET', '/viewer/session', headers=headers)
        self.assertEqual(json.loads(payload), {'automatic': True})
        status, _, payload = self.request('POST', '/v1/contexts/list', {'limit': 100}, headers)
        self.assertEqual(status, 200)
        self.assertEqual(self.calls[0][1], 'Bearer ct_scoped-viewer')
        self.assertNotIn(b'ct_scoped-viewer', payload)
        self.assertNotIn(self.key.encode(), payload)
        self.assertEqual(self.request('POST', '/v1/messages/replay', {'context_id': 'private-child'}, headers)[0], 403)

    def test_wrong_key_and_forged_session_are_denied(self):
        self.assertEqual(self.request('POST', '/viewer/login', {'key': 'wrong'})[0], 401)
        self.assertEqual(self.request('POST', '/viewer/login', {'key': '非ASCII'})[0], 401)
        self.assertEqual(self.request('POST', '/v1/contexts/list', {}, {'X-Comitia-Viewer-Session': 'forged'})[0], 401)
        self.assertEqual(self.calls, [])

    def test_host_origin_and_read_allowlist_survive_login(self):
        headers = self.login()
        for extra in ({'Host': 'evil.test'}, {'Origin': 'http://evil.test'}, {'Sec-Fetch-Site': 'cross-site'}):
            self.assertEqual(self.request('POST', '/v1/contexts/list', {}, headers | extra)[0], 403)
            self.assertEqual(self.request('POST', '/viewer/login', {'key': self.key}, extra)[0], 403)
        for path in ('/v1/messages/append', '/v1/sessions/delegate'):
            self.assertEqual(self.request('POST', path, {}, headers)[0], 404)
        self.assertEqual(self.calls, [])

    def test_explicit_credentials_do_not_fall_back_to_viewer(self):
        headers = self.login() | {'Authorization': 'Bearer ct_manual'}
        self.assertEqual(self.request('POST', '/v1/contexts/list', {}, headers)[0], 200)
        self.assertEqual(self.calls[-1][1], 'Bearer ct_manual')

    def test_restart_invalidates_browser_session(self):
        headers = self.login()
        self.stop_viewer()
        self.start_viewer()
        self.assertEqual(self.request('POST', '/v1/contexts/list', {}, headers)[0], 401)
        self.assertEqual(self.request('POST', '/v1/contexts/list', {}, self.login())[0], 200)

    def test_shared_credential_is_rejected_before_upstream_use(self):
        headers = self.login()
        self.credential.chmod(0o644)
        self.assertEqual(self.request('POST', '/v1/contexts/list', {}, headers)[0], 503)
        self.assertEqual(self.calls, [])

    def test_real_comitia_scope_and_revocation(self):
        binary = self.root/'comitia'
        build_env = dict(os.environ, GOTOOLCHAIN='go1.24.13', GOFLAGS='-buildvcs=false')
        subprocess.run(['go', 'build', '-o', str(binary), './cmd/comitia'], cwd=ROOT,
                       env=build_env, check=True, capture_output=True)
        env = {k: v for k, v in os.environ.items() if not k.startswith('COMITIA_')}
        env.update(HOME=str(self.root), COMITIA_BOOTSTRAP_CREDENTIAL='fixture-bootstrap')
        database = self.root/'fixture.db'
        def cli(*arguments, credential=''):
            result = subprocess.run([str(binary), '--db', str(database), *arguments],
                env=dict(env, COMITIA_CREDENTIAL=credential), check=True, capture_output=True, text=True)
            return json.loads(result.stdout)
        owner = cli('bootstrap', '--ttl', '24h')['credential']['Token']
        workspace = cli('workspace-create', '--name', 'fixture', credential=owner)['ID']
        context = cli('context-create', '--workspace-id', workspace, '--name', 'scoped', credential=owner)['ID']
        other = cli('context-create', '--workspace-id', workspace, '--name', 'other', credential=owner)['ID']
        child = cli('context-create', '--parent-id', context, '--name', 'private-child', credential=owner)['ID']
        observer = cli('delegate', '--context-id', context, '--permissions', 'read',
                       '--ttl', '2h', '--grant-ttl', '1h', credential=owner)
        with socket.socket() as probe:
            probe.bind(('127.0.0.1', 0))
            backend_port = probe.getsockname()[1]
        backend = subprocess.Popen([str(binary), '--db', str(database), 'serve', '--listen', f'127.0.0.1:{backend_port}'],
                                   env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        def stop_backend():
            backend.terminate()
            backend.wait(timeout=5)
        self.addCleanup(stop_backend)
        deadline = time.monotonic() + 5
        while True:
            try:
                with socket.create_connection(('127.0.0.1', backend_port), timeout=0.1):
                    break
            except OSError:
                if time.monotonic() > deadline:
                    self.fail('backend did not start')
                time.sleep(0.02)
        self.stop_viewer()
        self.credential.write_text(json.dumps(observer))
        # start_viewer uses only the upstream port, leaving the fixture lifecycle intact.
        real_port, self.upstream.server_port = self.upstream.server_port, backend_port
        try:
            self.start_viewer()
        finally:
            self.upstream.server_port = real_port
        headers = self.login()
        status, _, payload = self.request('POST', '/v1/contexts/list', {'limit': 100}, headers)
        self.assertEqual(status, 200, payload)
        self.assertEqual([c['ID'] for c in json.loads(payload)['contexts']], [context])
        self.assertEqual(self.request('POST', '/v1/messages/replay', {'context_id': context, 'limit': 100}, headers)[0], 200)
        for denied in (other, child, 'missing'):
            self.assertEqual(self.request('POST', '/v1/messages/replay', {'context_id': denied, 'limit': 100}, headers)[0], 403)
        # The read-only observer cannot write even when used directly upstream.
        connection = http.client.HTTPConnection('127.0.0.1', backend_port, timeout=3)
        try:
            connection.request('POST', '/v1/messages/append', json.dumps({'context_id': context, 'message': {'DedupeKey': 'denied', 'Body': 'denied'}}),
                               {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + observer['credential']['Token']})
            response = connection.getresponse()
            self.assertEqual(response.status, 403)
            response.read()
        finally:
            connection.close()
        cli('revoke', '--session-id', observer['credential']['Session']['ID'], credential=owner)
        self.assertEqual(self.request('POST', '/v1/contexts/list', {'limit': 100}, headers)[0], 401)

    def test_private_files_are_never_served(self):
        self.assertEqual(self.request('GET', '/viewer-launch-key')[0], 404)
        self.assertEqual(self.request('GET', '/viewer.json')[0], 404)

class LauncherTest(unittest.TestCase):
    def test_private_launcher_handoff_and_file_validation(self):
        viewer = ROOT/'tools/world-viewer'
        with mock.patch.object(sys, 'path', [str(viewer), *sys.path]):
            module = runpy.run_path(str(viewer/'launch.py'))
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root/'credentials').mkdir()
            config = root/'service.json'
            config.write_text(json.dumps({'viewer_url': 'http://127.0.0.1:4174'}))
            config.chmod(0o600)
            key = root/'credentials/viewer-launch-key'
            key.write_text('k' * 43)
            key.chmod(0o600)
            with mock.patch.object(module['webbrowser'], 'open') as browser:
                module['launch'](root)
                browser.assert_called_once_with('http://127.0.0.1:4174/#viewer-key=' + 'k' * 43)
                key.chmod(0o644)
                with self.assertRaises(ValueError):
                    module['launch'](root)
                key.chmod(0o600)
                target = root/'private-key'
                key.rename(target)
                key.symlink_to(target)
                with self.assertRaises(OSError):
                    module['launch'](root)
                self.assertEqual(browser.call_count, 1)

if __name__ == '__main__':
    unittest.main()
