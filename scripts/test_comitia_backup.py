"""Backup/restore boundaries exercised with a real binary and temporary installation."""
import json
import os
from pathlib import Path
import shutil
import sqlite3
import subprocess
import sys
import unittest

from scripts import test_install_linux_service as helpers
import comitia_install as local
import install_linux_service as installer

SCRIPT = helpers.SCRIPTS/'comitia_backup.py'


class BackupTest(helpers.Base):
    setUpClass = classmethod(helpers.RealBinaryTest.setUpClass.__func__)
    tearDownClass = classmethod(helpers.RealBinaryTest.tearDownClass.__func__)

    def setUp(self):
        super().setUp()
        self.context = installer.install(self.root, self.home, binary=self.binary,
                                         service=None, check_port=False)
        self.db = self.root/'data/comitia.db'
        self.run = local.runner(self.root/'bin/comitia', self.db)
        self.owner = local.token(self.root/'credentials/owner.json')
        self.worker = local.token(self.root/'credentials/codex.json')
        self.cid = self.context['context_id']
        self.bundle = Path(self.tmp.name)/'bundle'
        self.restored = Path(self.tmp.name)/'restored'

    def command(self, *args, error=None):
        result = subprocess.run([sys.executable, str(SCRIPT), *map(str, args)],
                                capture_output=True, text=True)
        if error:
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(error, result.stderr)
            return
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def snapshot(self, *args):
        return self.command('snapshot', '--root', self.root, '--output', self.bundle, *args)

    def drill_restore(self, *args, error=None):
        return self.command('restore', '--snapshot', self.bundle, '--output', self.restored,
                            *args, error=error)

    def test_online_backup_includes_uncheckpointed_wal_and_matching_private_files(self):
        # Keep a connection open so the command's last close cannot checkpoint the WAL.
        with sqlite3.connect(self.db) as held:
            held.execute('SELECT count(*) FROM messages').fetchone()
            self.run('send', '--context-id', self.cid, '--dedupe-key', 'committed',
                     '--body', 'in WAL', token=self.worker)
            self.assertGreater(Path(str(self.db)+'-wal').stat().st_size, 0)
            unsafe = Path(self.tmp.name)/'unsafe.db'
            shutil.copyfile(self.db, unsafe)
            with sqlite3.connect(unsafe) as copied:
                self.assertEqual(copied.execute('SELECT count(*) FROM messages').fetchone()[0], 0)
            old_umask = os.umask(0)
            try:
                self.snapshot('--config', self.home/'.codex/config.toml')
                report = self.drill_restore()
            finally:
                os.umask(old_umask)
        self.assertEqual(report['checks']['message_sequences'], [1])
        for base in (self.bundle, self.restored):
            for path in [base, *base.rglob('*')]:
                self.assertEqual(helpers.mode(path), 0o700 if path.is_dir() or path == base/'bin/comitia' else 0o600, path)
            self.assertEqual((base/'credentials/codex.json').read_bytes(),
                             (self.root/'credentials/codex.json').read_bytes())
            self.assertEqual((base/'local-context.json').read_bytes(), (self.root/'local-context.json').read_bytes())
            self.assertEqual((base/'config/0').read_bytes(), (self.home/'.codex/config.toml').read_bytes())
        self.assertFalse((self.bundle/'data/comitia.db-wal').exists())

    def test_restore_preserves_auth_grant_ancestry_instance_cursor_and_task(self):
        self.run('send', '--context-id', self.cid, '--dedupe-key', 'one', '--body', 'durable', token=self.worker)
        self.run('ack', '--context-id', self.cid, '--sequence', '1', '--agent-instance', 'worker', token=self.worker)
        task = self.run('task-create', '--context-id', self.cid, '--title', 'restore', token=self.worker)
        claim = self.run('task-claim', '--task-id', task['ID'], '--lease', '10m',
                         '--agent-instance', 'worker', token=self.worker)
        self.snapshot()
        report = self.drill_restore('--credential', 'codex', '--agent-instance', 'worker')['checks']
        self.assertEqual(report['cursor'], 1)
        self.assertEqual(report['message_sequences'], [1])
        self.assertEqual(report['tasks'][0]['LeaseVersion'], claim['Task']['LeaseVersion'])
        self.assertEqual(report['tasks'][0]['lease_owner_instance']['id'], 'worker')
        restored = local.runner(self.restored/'bin/comitia', self.restored/'data/comitia.db')
        self.assertEqual(restored('cursor', '--context-id', self.cid, '--agent-instance', 'other',
                                  token=self.worker)['sequence'], 0)

    def test_drill_exposes_lease_revocation_and_ack_rollback_before_reconciliation(self):
        self.run('send', '--context-id', self.cid, '--dedupe-key', 'one', '--body', 'durable', token=self.worker)
        task = self.run('task-create', '--context-id', self.cid, '--title', 'effect', token=self.worker)
        old = self.run('task-claim', '--task-id', task['ID'], '--lease', '10m', token=self.worker)
        self.snapshot()
        external_receipt = Path(self.tmp.name)/'external-effect-receipt'
        external_receipt.write_text('already performed after snapshot')
        new = self.run('task-renew', '--task-id', task['ID'], '--claim-token', old['Token'],
                       '--lease-version', str(old['Task']['LeaseVersion']), '--lease', '10m', token=self.worker)
        self.assertGreater(new['Task']['LeaseVersion'], old['Task']['LeaseVersion'])
        receipt = ('task-complete', '--task-id', task['ID'], '--claim-token', old['Token'],
                   '--lease-version', str(old['Task']['LeaseVersion']))
        with self.assertRaises(RuntimeError):
            self.run(*receipt, token=self.worker)
        self.run('ack', '--context-id', self.cid, '--sequence', '1', '--agent-instance', 'worker', token=self.worker)
        sid = json.loads((self.root/'credentials/codex.json').read_text())['credential']['Session']['ID']
        self.run('revoke', '--session-id', sid, token=self.owner)
        report = self.drill_restore('--credential', 'codex', '--agent-instance', 'worker')
        self.assertEqual(report['checks']['cursor'], 0)
        self.assertTrue(report['reconciliation_required'])
        self.assertEqual(report['checks']['tasks'][0]['Status'], 'leased')
        self.assertEqual(external_receipt.read_text(), 'already performed after snapshot')
        restored = local.runner(self.restored/'bin/comitia', self.restored/'data/comitia.db')
        self.assertEqual(restored(*receipt, token=self.worker)['Status'], 'completed')
        # In another untouched clone, fencing + admin release invalidates the old receipt.
        shutil.rmtree(self.restored)
        self.drill_restore()
        released = restored('task-release', '--task-id', task['ID'],
                            '--lease-version', str(old['Task']['LeaseVersion']), token=self.owner)
        self.assertEqual(released['Status'], 'pending')
        with self.assertRaises(RuntimeError):
            restored(*receipt, token=self.worker)
        # Reconciliation invalidates restored worker sessions before resumption.
        restored('revoke', '--session-id', sid, token=self.owner)
        with self.assertRaises(RuntimeError):
            restored('replay', '--context-id', self.cid, token=self.worker)

    def test_snapshot_rejects_expired_or_revoked_admin_and_cleans_partial_bundle(self):
        sid = json.loads((self.root/'credentials/owner.json').read_text())['credential']['Session']['ID']
        for mutation in ("UPDATE sessions SET expires_at='2000-01-01T00:00:00Z' WHERE id=?",
                         "UPDATE sessions SET revoked_at='2000-01-01T00:00:00Z' WHERE id=?"):
            with self.subTest(mutation=mutation):
                with sqlite3.connect(self.db) as db:
                    db.execute('UPDATE sessions SET expires_at=?, revoked_at=NULL WHERE id=?',
                               (json.loads((self.root/'credentials/owner.json').read_text())['credential']['Session']['ExpiresAt'], sid))
                    db.execute(mutation, (sid,))
                self.command('snapshot', '--root', self.root, '--output', self.bundle, error='unusable')
                self.assertFalse(self.bundle.exists())

    def test_snapshot_requires_matching_admin_but_accepts_explicit_matching_replacement(self):
        other_root = self.home/'other'
        installer.install(other_root, self.home, binary=self.binary, names=[], service=None, check_port=False)
        self.command('snapshot', '--root', self.root, '--output', self.bundle,
                     '--owner-credential', other_root/'credentials/owner.json', error='unusable')
        self.assertFalse(self.bundle.exists())
        self.command('snapshot', '--root', self.root, '--output', self.bundle,
                     '--owner-credential', self.root/'credentials/codex.json', error='administrator')
        self.assertFalse(self.bundle.exists())
        self.snapshot('--owner-credential', self.root/'credentials/owner.json')
        self.drill_restore()

    def test_snapshot_uses_existing_installation_flock(self):
        with local.installation_lock(self.root):
            self.command('snapshot', '--root', self.root, '--output', self.bundle, error='busy')
        self.assertFalse(self.bundle.exists())

    def test_restore_refuses_existing_directory_and_live_installation_paths(self):
        self.snapshot()
        marker = self.restored/'keep'
        self.restored.mkdir()
        marker.write_text('untouched')
        self.drill_restore(error='exists')
        self.assertEqual(marker.read_text(), 'untouched')
        self.command('restore', '--snapshot', self.bundle, '--output', self.root/'data/recovery', error='installation')
        self.assertFalse((self.root/'data/recovery').exists())

    def test_restore_rejects_changed_binary_and_unsafe_manifest_paths(self):
        self.snapshot()
        binary = self.bundle/'bin/comitia'
        changed = binary.with_suffix('.changed')
        changed.write_bytes(binary.read_bytes()+b'changed')
        os.replace(changed, binary)
        self.drill_restore(error='checksum')
        self.assertFalse(self.restored.exists())
        manifest = self.bundle/'manifest.json'
        value = json.loads(manifest.read_text())
        value['files']['../outside'] = 'invalid'
        manifest.write_text(json.dumps(value))
        self.drill_restore(error='Unsafe')
        self.assertFalse(self.restored.exists())

    def test_snapshot_checks_integrity_and_foreign_keys(self):
        with sqlite3.connect(self.db) as db:
            db.execute("UPDATE contexts SET workspace_id='missing' WHERE id=?", (self.cid,))
        self.command('snapshot', '--root', self.root, '--output', self.bundle, error='foreign key')
        self.assertFalse(self.bundle.exists())
        self.db.write_bytes(b'corrupt database')
        self.command('snapshot', '--root', self.root, '--output', self.bundle, error='database')
        self.assertFalse(self.bundle.exists())

    def test_restore_rechecks_expiry_and_rejects_symlinked_bundle_files(self):
        self.snapshot()
        # SHA hashes detect damage; they are not a replacement for authentication.
        dbpath = self.bundle/'data/comitia.db'
        with sqlite3.connect(dbpath) as db:
            db.execute("UPDATE sessions SET expires_at='2000-01-01T00:00:00Z'")
        import hashlib
        manifest = self.bundle/'manifest.json'
        value = json.loads(manifest.read_text())
        value['files']['data/comitia.db'] = hashlib.sha256(dbpath.read_bytes()).hexdigest()
        manifest.write_text(json.dumps(value))
        self.drill_restore(error='unusable')
        self.assertFalse(self.restored.exists())
        credential = self.bundle/'credentials/owner.json'
        credential.unlink()
        credential.symlink_to(self.root/'credentials/owner.json')
        self.drill_restore(error='symlink')
        self.assertFalse(self.restored.exists())

    def test_invalid_delegate_is_reported_and_cannot_be_selected_for_drill(self):
        document = json.loads((self.root/'credentials/codex.json').read_text())
        self.run('grant-revoke', '--grant-id', document['grants'][0]['ID'], token=self.owner)
        self.snapshot()
        report = self.drill_restore()['checks']
        self.assertIn('codex', report['unusable_credentials'])
        shutil.rmtree(self.restored)
        self.drill_restore('--credential', 'codex', error='unusable')

    def test_documented_commands_and_reconciliation_floor_are_published(self):
        guide = (helpers.SCRIPTS.parent/'docs/backup-restore.md').read_text()
        for text in ('comitia_backup.py snapshot', 'comitia_backup.py restore', 'task-release',
                     'revoke', 'rotate-credentials', 'external', 'rollback floor', 'quiesce',
                     '--agent-instance', '--credential'):
            self.assertIn(text, guide)
        for path in ('docs/security.md', 'docs/RUNBOOK.md', 'docs/local-service.md'):
            self.assertIn('backup-restore.md', (helpers.SCRIPTS.parent/path).read_text())


if __name__ == '__main__':
    unittest.main()
