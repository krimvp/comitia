#!/usr/bin/env python3
"""Private online snapshots and isolated restore drills; never start or replace a service."""
import argparse
import contextlib
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import sqlite3
import sys

import comitia_install as local

FORMAT = 1
CONFIG = ('local-context.json', 'harnesses.json', 'service.json', 'INSTALLATION.txt')


@contextlib.contextmanager
def readonly(path):
    with contextlib.closing(sqlite3.connect(Path(path).resolve().as_uri()+'?mode=ro', uri=True)) as db:
        yield db


def digest(path):
    with Path(path).open('rb') as stream:
        return hashlib.file_digest(stream, 'sha256').hexdigest()


def copy_private(source, target, executable=False):
    source, target = Path(source), Path(target)
    if source.is_symlink() or not source.is_file():
        raise RuntimeError(f'Not a regular file (or symlink): {source}')
    target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd = os.open(target, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o700 if executable else 0o600)
    with source.open('rb') as src, os.fdopen(fd, 'wb') as dst:
        shutil.copyfileobj(src, dst)


def verify_database(database):
    with readonly(database) as db:
        if list(db.execute('PRAGMA integrity_check')) != [('ok',)]:
            raise RuntimeError('SQLite integrity check failed')
        if list(db.execute('PRAGMA foreign_key_check')):
            raise RuntimeError('SQLite foreign key check failed')


def checks(root, credential='owner', instance=''):
    """Authenticate against the clone, including effective ancestry, without network routing."""
    database = root/'data/comitia.db'
    verify_database(database)
    context_id = json.loads((root/'local-context.json').read_text())['context_id']
    run = local.runner(root/'bin/comitia', database)
    owner = root/'credentials/owner.json'
    local.validate_credential(run, owner)
    # A readable delegate named owner is not an administrator recovery credential.
    with readonly(database) as db:
        row = db.execute('SELECT p.admin FROM sessions s JOIN principals p ON p.id=s.principal_id '
                         'WHERE s.token_hash=?', (hashlib.sha256(local.token(owner).encode()).hexdigest(),)).fetchone()
        if not row or not row[0]:
            raise RuntimeError('The matching owner credential must be an administrator')
    unusable = []
    for path in sorted((root/'credentials').glob('*.json')):
        try:
            local.validate_credential(run, path, context_id)
        except RuntimeError:
            unusable.append(path.stem)
    if not local.NAME.fullmatch(credential):
        raise RuntimeError('Invalid credential name')
    selected = root/'credentials'/f'{credential}.json'
    local.validate_credential(run, selected, context_id)
    secret = local.token(selected)
    info = run('server-info', token=local.token(owner))
    messages = run('replay', '--context-id', context_id, '--limit', '1',
                   '--agent-instance', instance, token=secret) or []
    cursor = run('cursor', '--context-id', context_id, '--agent-instance', instance, token=secret)['sequence']
    # task-list is capped; get every task in this context rather than silently truncating leases.
    with readonly(database) as db:
        ids = [r[0] for r in db.execute('SELECT id FROM tasks WHERE context_id=? ORDER BY id', (context_id,))]
    tasks = []
    for task_id in ids:
        task = run('task-get', '--task-id', task_id, token=secret)
        tasks.append({k: v for k, v in task.items() if k in (
            'ID', 'Status', 'LeaseOwnerSessionID', 'LeaseVersion', 'LeaseExpiresAt', 'lease_owner_instance')})
    return {'integrity': 'ok', 'foreign_keys': 'ok', 'server_info': info,
            'context_id': context_id, 'credential': credential, 'agent_instance': instance,
            'unusable_credentials': unusable, 'message_sequences': [m['Sequence'] for m in messages],
            'cursor': cursor, 'tasks': tasks}


def new_output(path):
    path = Path(path).absolute()
    if path.exists() or path.is_symlink():
        raise RuntimeError(f'Output {path} already exists; choose a new directory')
    # Require an existing parent so a typo cannot create a second installation tree.
    path.mkdir(mode=0o700)
    return path


@local.locked_installation(0)
def snapshot(root, output, owner_credential=None, configs=()):
    root = Path(root).resolve()
    output = Path(output).absolute()
    if output.resolve() == root or root.is_relative_to(output.resolve()):
        raise RuntimeError('Snapshot output cannot contain the installation')
    destination = new_output(output)
    try:
        for name in CONFIG:
            source = root/name
            if source.exists():
                copy_private(source, destination/name)
        for source in sorted((root/'credentials').glob('*.json')):
            if source.name != 'owner.json':
                copy_private(source, destination/'credentials'/source.name)
        copy_private(owner_credential or root/'credentials/owner.json', destination/'credentials/owner.json')
        copy_private(root/'bin/comitia', destination/'bin/comitia', executable=True)
        saved_configs = {}
        for i, source in enumerate(configs):
            name = f'config/{i}'
            copy_private(source, destination/name)
            saved_configs[name] = str(Path(source).absolute())
        database = destination/'data/comitia.db'
        database.parent.mkdir(mode=0o700)
        fd = os.open(database, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        os.close(fd)
        with readonly(root/'data/comitia.db') as source, contextlib.closing(sqlite3.connect(database)) as target:
            source.backup(target)
        report = checks(destination)
        # The drill can write presence. Fold its WAL into the clone; ship one portable DB.
        with contextlib.closing(sqlite3.connect(database)) as db:
            if db.execute('PRAGMA wal_checkpoint(TRUNCATE)').fetchone()[0] != 0:
                raise RuntimeError('Snapshot database checkpoint is busy')
            if db.execute('PRAGMA journal_mode=DELETE').fetchone()[0] != 'delete':
                raise RuntimeError('Snapshot database could not leave WAL mode')
        files = {str(p.relative_to(destination)): digest(p) for p in sorted(destination.rglob('*')) if p.is_file()}
        manifest = {'format': FORMAT, 'created_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                    'source_root': str(root), 'files': files, 'external_configs': saved_configs,
                    'rollback_floor': report['server_info']['build'], 'checks': report}
        # The manifest is the completion marker; a partial bundle is not restorable.
        local.private_json(destination/'manifest.json', manifest)
        return {'snapshot': str(destination), 'checks': report}
    except BaseException:
        shutil.rmtree(destination)
        raise


def validate_bundle(bundle):
    bundle = Path(bundle).resolve()
    manifest = bundle/'manifest.json'
    if manifest.is_symlink():
        raise RuntimeError('Snapshot manifest is a symlink')
    value = json.loads(manifest.read_text())
    if value.get('format') != FORMAT:
        raise RuntimeError('Unsupported snapshot format')
    files = value['files']
    required = {'bin/comitia', 'data/comitia.db', 'credentials/owner.json', 'local-context.json'}
    if not required.issubset(files):
        raise RuntimeError('Incomplete snapshot: required files missing')
    # Validate every path before opening anything, even if an earlier checksum is wrong.
    for name in files:
        path = Path(name)
        if path.is_absolute() or '..' in path.parts or str(path) != name or name == 'manifest.json':
            raise RuntimeError(f'Unsafe snapshot path: {name}')
        candidate = bundle/path
        if candidate.is_symlink() or any(p.is_symlink() for p in candidate.parents if p != bundle and p.is_relative_to(bundle)):
            raise RuntimeError(f'Snapshot path is a symlink: {name}')
    for name, expected in files.items():
        if not (bundle/name).is_file() or digest(bundle/name) != expected:
            raise RuntimeError(f'Snapshot checksum mismatch: {name}')
    return bundle, value


def restore(bundle, output, credential='owner', instance=''):
    bundle, manifest = validate_bundle(bundle)
    output = Path(output).absolute()
    source_root = Path(manifest['source_root']).resolve()
    resolved = output.resolve()
    if (resolved.is_relative_to(source_root) or source_root.is_relative_to(resolved) or
            resolved.is_relative_to(bundle) or bundle.is_relative_to(resolved)):
        raise RuntimeError('Restore output must be separate from the installation and snapshot')
    destination = new_output(output)
    try:
        for name in manifest['files']:
            copy_private(bundle/name, destination/name, executable=name == 'bin/comitia')
        report = checks(destination, credential, instance)
        value = {'restored': str(destination), 'checks': report, 'reconciliation_required': True,
                 'snapshot_created_at': manifest['created_at'], 'rollback_floor': manifest['rollback_floor']}
        local.private_json(destination/'restore-report.json', value)
        local.private_json(destination/'manifest.json', manifest)
        return value
    except BaseException:
        shutil.rmtree(destination)
        raise


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest='command', required=True)
    save = commands.add_parser('snapshot', help='Online SQLite snapshot with matching private credentials/config')
    save.add_argument('--root', type=Path, required=True, help='Installation root; no HOME discovery')
    save.add_argument('--output', type=Path, required=True, help='New snapshot directory, parent must exist')
    save.add_argument('--owner-credential', type=Path, help='Another still-valid admin credential for this database')
    save.add_argument('--config', type=Path, action='append', default=[], help='Additional private deployment/harness config (repeatable)')
    drill = commands.add_parser('restore', help='Copy and validate in isolation; never start a service')
    drill.add_argument('--snapshot', type=Path, required=True, help='Trusted private snapshot directory')
    drill.add_argument('--output', type=Path, required=True, help='New isolated directory outside the installation')
    drill.add_argument('--credential', default='owner', help='Saved credential name used for replay/cursor/task checks')
    drill.add_argument('--agent-instance', default='', help='Saved instance cursor to check (default: legacy session cursor)')
    args = parser.parse_args(argv)
    if args.command == 'snapshot':
        result = snapshot(args.root, args.output, args.owner_credential, args.config)
    else:
        result = restore(args.snapshot, args.output, args.credential, args.agent_instance)
    print(json.dumps(result, indent=2))
    return result


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, KeyError, TypeError, RuntimeError, sqlite3.Error) as error:
        raise SystemExit(f'error: {error}')
