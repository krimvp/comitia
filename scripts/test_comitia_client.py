import datetime
import json
import os
from pathlib import Path
import sys
import tempfile
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
        with self.assertRaises(SystemExit):
            client.sessions(['--prune'], self.root)
        self.assertTrue(outside.exists())
        path.unlink()
        self.directory.rmdir()
        self.directory.symlink_to(self.root, target_is_directory=True)
        with self.assertRaises(SystemExit):
            client.sessions(['--prune'], self.root)

    def test_refuses_public_file_and_invalid_options(self):
        path, _ = self.credential('public', -60)
        path.chmod(0o644)
        with self.assertRaises(SystemExit):
            client.sessions(['--prune'], self.root)
        self.assertTrue(path.exists())
        with self.assertRaises(SystemExit):
            client.sessions(['--prune', '../outside'], self.root)

    def test_missing_directory(self):
        self.directory.rmdir()
        self.assertEqual(client.sessions([], self.root), [])
