#!/usr/bin/env python3
"""Throwaway localhost prototype. Only allowlisted read operations are proxied."""
import argparse
import functools
import http.server
import json
from pathlib import Path
import urllib.error
import urllib.parse
import urllib.request

parser = argparse.ArgumentParser()
parser.add_argument('--port', type=int, default=4173)
parser.add_argument('--upstream', default='http://127.0.0.1:8080')
args = parser.parse_args()
upstream = urllib.parse.urlparse(args.upstream)
if upstream.scheme not in ('http', 'https') or not upstream.hostname or upstream.username or upstream.password or upstream.query or upstream.fragment:
    parser.error('Use an HTTP(S) server URL without credentials, query, or fragment.')
ALLOWED = {'/v1/contexts/list', '/v1/participants/list', '/v1/messages/replay'}
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None
opener = urllib.request.build_opener(NoRedirect)
class Handler(http.server.SimpleHTTPRequestHandler):
    def end_headers(self):
        self.send_header('Cache-Control', 'no-store')
        self.send_header('X-Content-Type-Options', 'nosniff')
        self.send_header('Referrer-Policy', 'no-referrer')
        super().end_headers()
    def do_POST(self):
        origin = self.headers.get('Origin')
        if origin and origin != 'http://' + self.headers.get('Host', ''):
            self.send_error(403); return
        if self.path not in ALLOWED:
            self.send_error(404); return
        try:
            length = int(self.headers.get('Content-Length', '0'))
            if not 0 < length <= 65536:
                self.send_error(413); return
            body = self.rfile.read(length)
            json.loads(body)
            request = urllib.request.Request(args.upstream.rstrip('/') + self.path, data=body,
                headers={'Content-Type': 'application/json', 'Authorization': self.headers.get('Authorization', '')}, method='POST')
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
