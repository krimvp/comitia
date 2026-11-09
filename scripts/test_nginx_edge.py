"""Static guards for the public nginx edge. Run: python3 -m unittest scripts/test_nginx_edge.py."""
from pathlib import Path
import re
import unittest

DEPLOY = Path(__file__).resolve().parents[1]/'deploy'
MAIN, ACME = DEPLOY/'comitia-mcp.nginx.conf', DEPLOY/'comitia-mcp-acme.nginx.conf'
HOST = 'comitia.example.com'
# Variables that can carry a URL-embedded credential into an access log.
URL_VARS = re.compile(r'\$(request|request_uri|uri|document_uri|args|arg_\w+|query_string|'
                      r'http_referer|upstream_\w+|request_line)\b')


def config(path):
    return '\n'.join(line.split('#', 1)[0] for line in path.read_text().splitlines())


def blocks(text, head):
    """Top-level `head { ... }` bodies, matched by braces."""
    found = []
    for match in re.finditer(r'(?m)^\s*' + head + r'\s*\{', text):
        depth, i = 1, match.end()
        while depth:
            depth += {'{': 1, '}': -1}.get(text[i], 0)
            i += 1
        found.append(text[match.end():i - 1])
    return found


def servers(path):
    return blocks(config(path), 'server')


def port80(path):
    return [s for s in servers(path) if re.search(r'listen 80;', s)]


class NginxEdgeTest(unittest.TestCase):
    def test_access_log_formats_exclude_request_urls(self):
        for path in (MAIN, ACME):
            formats = re.findall(r'log_format\s+(\w+)((?:\s+\'[^\']*\')+)\s*;', config(path))
            self.assertTrue(formats, path.name)
            for name, body in formats:
                self.assertIsNone(URL_VARS.search(body), f'{path.name}: {name}')
            names = {name for name, _ in formats}
            for server in servers(path):
                logs = re.findall(r'access_log\s+(\S+)(?:\s+(\w+))?\s*;', server)
                self.assertTrue(logs, f'{path.name}: every server needs a credential-free access_log')
                for target, fmt in logs:
                    self.assertTrue(target == 'off' or fmt in names, f'{path.name}: {target} {fmt}')

    def test_path_credential_locations_discard_error_log(self):
        locations = [loc for server in servers(MAIN) for loc in blocks(server, r'location ~ \^/mcp/\S+')]
        self.assertEqual(len(locations), 2)
        for location in locations:
            self.assertRegex(location, r'(?m)^\s*error_log /dev/null;')
            self.assertIn('proxy_pass http://127.0.0.1:8391;', location)
        # Only the path form loses its error log; the header route keeps it.
        for server in servers(MAIN):
            for location in blocks(server, r'location = /mcp'):
                self.assertNotIn('error_log', location)

    def test_every_server_refuses_unknown_host_before_routing(self):
        for path in (MAIN, ACME):
            for server in servers(path):
                guard = server.find(f'if ($host != "{HOST}") {{\n        return 404;')
                self.assertGreater(guard, -1, path.name)
                self.assertLess(guard, server.find('location'), path.name)
                self.assertIn(f'server_name {HOST};', server)

    def test_tls_servers_send_hsts(self):
        tls = [s for s in servers(MAIN) if ' ssl;' in s]
        self.assertEqual(len(tls), 2)
        for server in tls:
            self.assertRegex(server, r'add_header Strict-Transport-Security "max-age=\d+" always;')

    def test_port_80_refuses_mcp_and_matches_acme_block(self):
        main, acme = port80(MAIN), port80(ACME)
        self.assertEqual((len(main), len(acme)), (1, 1))
        self.assertEqual(main[0], acme[0].replace('comitia_mcp_acme_safe', 'comitia_mcp_safe'))
        self.assertRegex(main[0], r'location \^~ /mcp \{\s*return 403;\s*\}')
        for location in blocks(main[0], r'location /'):
            self.assertNotIn(':8443', location)
        self.assertNotIn('ssl', main[0])


if __name__ == '__main__':
    unittest.main()
