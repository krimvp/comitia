#!/usr/bin/env python3
"""Throwaway localhost prototype. Only allowlisted read operations are proxied."""
import argparse
import functools
import http.server
import json
import secrets
import threading
import time
from private_files import private_text
from pathlib import Path
import urllib.error
import urllib.parse
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('--port', type=int, default=4173)
parser.add_argument('--upstream', default='http://127.0.0.1:8080')
parser.add_argument('--credential-file', type=Path, help='Private local credential JSON, kept server-side')
parser.add_argument('--launch-key-file', type=Path, help='Private launcher key; defaults to viewer-launch-key beside the credential')
args = parser.parse_args()
upstream = urllib.parse.urlparse(args.upstream)
if upstream.scheme not in ('http', 'https') or not upstream.hostname or upstream.username or upstream.password or upstream.query or upstream.fragment:
    parser.error('Use an HTTP(S) server URL without credentials, query, or fragment.')
root = Path(__file__).resolve().parent
if args.credential_file:
    args.credential_file = args.credential_file.expanduser().absolute()
    if root == args.credential_file.resolve() or root in args.credential_file.resolve().parents:
        parser.error('The credential must be outside the served asset directory.')
    if upstream.hostname not in ('127.0.0.1', 'localhost', '::1'):
        parser.error('Automatic credentials require a loopback upstream.')
    args.launch_key_file = args.launch_key_file or args.credential_file.parent/'viewer-launch-key'
    # Do not resolve symlinks before private_text's O_NOFOLLOW check.
    if root == args.launch_key_file.resolve() or root in args.launch_key_file.resolve().parents:
        parser.error('The launcher key must be outside the served asset directory.')
    try:
        launch_key = private_text(args.launch_key_file).strip()
        private_text(args.credential_file)
        if len(launch_key) < 32:
            raise ValueError('Launcher key must contain at least 32 characters')
    except (OSError, ValueError) as error:
        parser.error(str(error))
sessions = {}
session_lock = threading.Lock()
SESSION_TTL = 8 * 60 * 60
ALLOWED = {'/v1/contexts/list', '/v1/participants/list', '/v1/messages/replay'}
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None
opener = urllib.request.build_opener(NoRedirect)
class Handler(http.server.SimpleHTTPRequestHandler):
    def local_request(self):
        host = self.headers.get('Host', '')
        if host not in (f'127.0.0.1:{args.port}', f'localhost:{args.port}'):
            self.send_error(403); return False
        origin = self.headers.get('Origin')
        if origin and origin != 'http://' + host:
            self.send_error(403); return False
        if self.headers.get('Sec-Fetch-Site') not in (None, 'same-origin', 'none'):
            self.send_error(403); return False
        return True
    def viewer_session(self):
        session = self.headers.get('X-Comitia-Viewer-Session', '')
        with session_lock:
            return sessions.get(session, 0) > time.monotonic()
    def json_response(self, status, value):
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.end_headers()
        self.wfile.write(json.dumps(value).encode())
    def do_GET(self):
        if not self.local_request(): return
        if self.path == '/viewer/session':
            self.json_response(200, {'automatic': bool(args.credential_file) and self.viewer_session()})
            return
        super().do_GET()
    def do_HEAD(self):
        if self.local_request(): super().do_HEAD()

    def end_headers(self):
        self.send_header('Cache-Control', 'no-store')
        self.send_header('X-Content-Type-Options', 'nosniff')
        self.send_header('Referrer-Policy', 'no-referrer')
        super().end_headers()
    def do_POST(self):
        if not self.local_request(): return
        if self.path not in ALLOWED and self.path != '/viewer/login':
            self.send_error(404); return
        try:
            length = int(self.headers.get('Content-Length', '0'))
            if not 0 < length <= 65536:
                self.send_error(413); return
            body = self.rfile.read(length)
            value = json.loads(body)
            if self.path == '/viewer/login':
                key = value.get('key') if isinstance(value, dict) else None
                if not args.credential_file or not isinstance(key, str) or not secrets.compare_digest(key.encode(), launch_key.encode()):
                    self.json_response(401, {'error': 'Viewer launcher authentication required.'})
                    return
                session = secrets.token_urlsafe(32)
                now = time.monotonic()
                with session_lock:
                    for old in list(sessions):
                        if sessions[old] <= now:
                            del sessions[old]
                    if len(sessions) >= 128:
                        del sessions[next(iter(sessions))]
                    sessions[session] = now + SESSION_TTL
                self.json_response(200, {'session': session})
                return
            authorization = self.headers.get('Authorization', '')
            if not authorization and args.credential_file:
                if not self.viewer_session():
                    self.json_response(401, {'error': 'Open the viewer using its private launcher.'})
                    return
                try:
                    credential = json.loads(private_text(args.credential_file))
                    saved_token = credential.get('credential', credential).get('Token')
                    if not isinstance(saved_token, str) or not saved_token.startswith('ct_'):
                        raise ValueError('Invalid credential')
                    authorization = 'Bearer ' + saved_token
                except (OSError, ValueError, AttributeError):
                    self.json_response(503, {'error': 'Local sign-in credential unavailable.'})
                    return
            request = urllib.request.Request(args.upstream.rstrip('/') + self.path, data=body,
                headers={'Content-Type': 'application/json', 'Authorization': authorization}, method='POST')
            with opener.open(request, timeout=12) as response:
                payload = response.read()
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers(); self.wfile.write(payload)
        except urllib.error.HTTPError as error:
            self.send_response(error.code)
            self.send_header('Content-Type', 'application/json')
            self.end_headers(); self.wfile.write(json.dumps({'error': 'Comitia request rejected', 'status': error.code}).encode())
        except (urllib.error.URLError, TimeoutError, OSError):
            self.send_response(502); self.send_header('Content-Type', 'application/json'); self.end_headers()
            self.wfile.write(b'{"error":"Comitia server unavailable. Check --upstream and that comitia serve is running."}')
        except (ValueError, json.JSONDecodeError):
            self.send_error(400)
    def list_directory(self, path):
        self.send_error(404)
    def log_message(self, fmt, *values):
        pass

root = Path(__file__).resolve().parent
server = http.server.ThreadingHTTPServer(('127.0.0.1', args.port), functools.partial(Handler, directory=str(root)))
print(f'Agent world: http://127.0.0.1:{args.port} — upstream: {args.upstream}', flush=True)
server.serve_forever()
