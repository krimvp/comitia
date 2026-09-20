#!/usr/bin/env python3
"""Import a legacy board snapshot via the persistent API; safe to rerun incrementally."""
import argparse
import fcntl
import hashlib
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import tempfile

ROOT = Path.home() / 'Library/Application Support/Comitia'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('database', type=Path)
    parser.add_argument('--label', required=True)
    opts = parser.parse_args()
    source = opts.database.expanduser().resolve(strict=True)
    source_key = hashlib.sha256(str(source).encode()).hexdigest()[:24]
    directory = ROOT / 'imports' / source_key
    directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    directory.chmod(0o700)
    os.umask(0o077)
    lock = (directory / 'lock').open('w')
    fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
    state_path = directory / 'state.json'
    state = json.loads(state_path.read_text()) if state_path.exists() else {'contexts': {}, 'agents': {}, 'messages': {}}
    config = json.loads((ROOT / 'service.json').read_text())
    if state.get('server_url', config['server_url']) != config['server_url']:
        raise RuntimeError('Import checkpoint belongs to another server.')
    state['server_url'] = config['server_url']
    state['source'] = str(source)
    def save():
        temporary = directory / 'state.new'
        temporary.write_text(json.dumps(state, indent=2)+'\n')
        temporary.replace(state_path)
    owner = json.loads((ROOT / 'credentials/owner.json').read_text())['credential']['Token']
    env = {k: v for k, v in os.environ.items() if not k.startswith('COMITIA_')}
    env['COMITIA_SERVER_URL'] = config['server_url']
    def run(*args, token=owner):
        result = subprocess.run([str(ROOT / 'bin/comitia'), *args], env={**env, 'COMITIA_CREDENTIAL': token}, capture_output=True, text=True)
        if result.returncode:
            raise RuntimeError(result.stderr.strip())
        return json.loads(result.stdout)
    # Opening an existing database with query_only permits SQLite to establish
    # WAL coordination files when absent. No application rows are modified.
    with sqlite3.connect(source.as_uri()+'?mode=rw', uri=True) as original:
        original.execute('PRAGMA query_only=ON')
        with sqlite3.connect(directory / 'snapshot.db') as snapshot:
            original.backup(snapshot)
    with sqlite3.connect(directory / 'snapshot.db') as db:
        db.row_factory = sqlite3.Row
        entries = [dict(r) for r in db.execute('SELECT * FROM entries ORDER BY seq')]
        archive = {table: [dict(r) for r in db.execute('SELECT * FROM '+table)] for table in ('workspaces', 'threads', 'claims')}
    # Preserve thread state and claims as historical data, never live task leases.
    (directory / 'history.json').write_text(json.dumps(archive, indent=2)+'\n')
    workspace = json.loads((ROOT / 'local-context.json').read_text())['workspace_id']
    for row in archive['workspaces']:
        ref = row['ref']
        if ref not in state['contexts']:
            context = run('context-create', '--workspace-id', workspace, '--name', f"{opts.label} · imported · {ref}")
            state['contexts'][ref] = context['ID']
            save()
    # A separate attributed session per legacy author; credentials stay private.
    for entry in entries:
        key = json.dumps([entry['workspace'], entry['author']])
        if key not in state['agents']:
            agent = run('delegate', '--context-id', state['contexts'][entry['workspace']], '--name', entry['author']+' (imported)', '--permissions', 'read,write', '--ttl', '720h', '--grant-ttl', '719h')
            state['agents'][key] = agent['credential']
            save()
    imported = 0
    for entry in entries:
        key = json.dumps([entry['workspace'], entry['id']])
        digest = hashlib.sha256(json.dumps(entry, sort_keys=True).encode()).hexdigest()
        if key in state['messages']:
            if state['messages'][key]['digest'] != digest:
                raise RuntimeError(f"Source entry {entry['id']} changed after import; refusing to silently overwrite history.")
            continue
        context = state['contexts'][entry['workspace']]
        agent = state['agents'][json.dumps([entry['workspace'], entry['author']])]
        recipients = json.loads(entry['to_list'])
        envelope = {'schema': 'comitia.discussion.v1', 'payload': {}}
        envelope['recipient_session_ids'] = [state['agents'][json.dumps([entry['workspace'], name])]['Session']['ID'] for name in recipients if json.dumps([entry['workspace'], name]) in state['agents']]
        body = f"Imported {entry['type']} · {entry['created_at']} · {entry['thread'] or 'workspace'}\n\n{entry['body']}"
        metadata = {k: v for k, v in entry.items() if k != 'body'}
        body += '\n\nImport details\n```json\n' + json.dumps(metadata, indent=2) + '\n```'
        with tempfile.NamedTemporaryFile(mode='w', suffix='.json', dir=directory) as file:
            json.dump(envelope, file)
            file.flush()
            message = run('send', '--context-id', context, '--dedupe-key', source_key+':'+entry['id'], '--body', body, '--envelope-file', file.name, token=agent['Token'])
        state['messages'][key] = {'id': message['ID'], 'digest': digest}
        save()
        imported += 1
    print(json.dumps({'imported': imported, 'total': len(state['messages']), 'contexts': state['contexts'], 'archive': str(directory)}))

if __name__ == '__main__':
    main()
