#!/usr/bin/env python3
"""Install Comitia and its world viewer as per-user macOS launchd services."""
import argparse
import datetime
import json
import os
from pathlib import Path
import plistlib
import shutil
import socket
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import comitia_install as local  # noqa: E402

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary', type=Path, help='Use an already built Comitia binary')
parser.add_argument('--port', type=int, default=8080)
parser.add_argument('--viewer-port', type=int, default=4174)
parser.add_argument('--rotate-credentials', action='store_true', help='Replace owner and local harness/viewer credentials before expiry')
parser.add_argument('--owner-credential', type=Path, help='Another active admin credential for rotation')
parser.add_argument('--revoke-old', action='store_true', help='Revoke old sessions and descendants after rotation')
args = parser.parse_args()
if sys.platform != 'darwin':
    parser.error('This installer requires macOS.')
if args.port == args.viewer_port or any(not 1024 <= p <= 65535 for p in (args.port, args.viewer_port)):
    parser.error('Choose distinct ports between 1024 and 65535.')
repo = Path(__file__).resolve().parent.parent
root = Path.home() / 'Library/Application Support/Comitia'
if args.rotate_credentials:
    archive = local.rotate_credentials(root, local.runner(root/'bin/comitia', root/'data/comitia.db'),
                                       args.owner_credential, args.revoke_old)
    print(f'Rotated local credentials. Originals: {archive}. Restart agent sessions and the viewer service.')
    sys.exit(0)
if args.owner_credential or args.revoke_old:
    parser.error('--owner-credential and --revoke-old require --rotate-credentials')
with local.installation_lock(root):
    launch_dir = Path.home() / 'Library/LaunchAgents'
    labels = ('com.comitia.server', 'com.comitia.viewer')
    domain = f'gui/{os.getuid()}'
    for port, label in zip((args.port, args.viewer_port), labels):
        installed = subprocess.run(['launchctl', 'print', f'{domain}/{label}'], capture_output=True).returncode == 0
        if not installed:
            with socket.socket() as probe:
                try:
                    probe.bind(('127.0.0.1', port))
                except OSError:
                    parser.error(f'Port {port} is already occupied. Choose another port.')
    for directory in (root, root/'bin', root/'data', root/'credentials', root/'logs', launch_dir):
        directory.mkdir(parents=True, exist_ok=True)
        if directory != launch_dir:
            directory.chmod(0o700)
    database = root/'data/comitia.db'
    binary = local.install_binary(root, args.binary)
    run = local.runner(binary, database)
    context = local.bootstrap(run, root, database)
    local.validate_install(run, root, context['context_id'])
    context['server_url'] = f'http://127.0.0.1:{args.port}'
    local.write_text(root/'local-context.json', json.dumps(context, indent=2)+'\n', mode=0o600)
    owner = root/'credentials/owner.json'
    if not (root/'credentials/viewer.json').exists():
        observer = run('delegate', '--name', 'World viewer', '--ttl', local.child_ttl(owner, datetime.timedelta(hours=744)),
                       '--context-id', context['context_id'], '--permissions', 'read',
                       '--grant-ttl', local.child_ttl(owner, datetime.timedelta(hours=720)), token=local.token(owner))
        local.private_json(root/'credentials/viewer.json', observer)
    # A reinstall never bootstraps or replaces an existing database or credential.
    viewer = root/'viewer'
    shutil.copytree(repo/'httpapi/ui/world-prototype', viewer, dirs_exist_ok=True,
                    ignore=shutil.ignore_patterns('__pycache__', '*.pyc'))
    # Install a stable entry point on the user's existing local-bin PATH.
    client = root/'bin/comitia-local'
    shutil.copy2(repo/'scripts/comitia_local.py', client)
    client.chmod(0o700)
    shutil.copy2(repo/'docs/local-sessions.md', root/'LOCAL-SESSIONS.md')
    (root/'LOCAL-SESSIONS.md').chmod(0o600)
    local_bin = Path.home()/'.local/bin'
    local_bin.mkdir(parents=True, exist_ok=True)
    for name in ('comitia', 'comitia-local'):
        link = local_bin/name
        if link.is_symlink() and link.resolve() == client:
            continue
        if link.exists() or link.is_symlink():
            raise RuntimeError(f'Refusing to replace an unrelated command: {link}')
        link.symlink_to(client)
    service_config = root/'service.json'
    service_config.write_text(json.dumps({'server_url':f'http://127.0.0.1:{args.port}',
                                          'viewer_url':f'http://127.0.0.1:{args.viewer_port}'}, indent=2)+'\n')
    service_config.chmod(0o600)
    configs = [
        {'Label': labels[0], 'ProgramArguments': [str(binary), '--db', str(database), 'serve', '--listen', f'127.0.0.1:{args.port}']},
        {'Label': labels[1], 'ProgramArguments': ['/usr/bin/python3', str(viewer/'serve.py'), '--port', str(args.viewer_port), '--upstream', f'http://127.0.0.1:{args.port}', '--credential-file', str(root/'credentials/owner.json')]}
    ]
    for config in configs:
        label = config['Label']
        config.update(RunAtLoad=True, KeepAlive=True, ThrottleInterval=5, WorkingDirectory=str(root),
                      Umask=0o077, StandardOutPath=str(root/'logs'/f'{label}.out.log'),
                      StandardErrorPath=str(root/'logs'/f'{label}.err.log'))
        path = launch_dir/f'{label}.plist'
        with path.open('wb') as stream:
            plistlib.dump(config, stream)
        path.chmod(0o600)
        subprocess.run(['plutil', '-lint', str(path)], check=True, capture_output=True)
        subprocess.run(['launchctl', 'bootout', f'{domain}/{label}'], capture_output=True)
        subprocess.run(['launchctl', 'enable', f'{domain}/{label}'], check=True)
        subprocess.run(['launchctl', 'bootstrap', domain, str(path)], check=True)
    print(f'Comitia server: http://127.0.0.1:{args.port}')
    print(f'Agent world: http://127.0.0.1:{args.viewer_port}')
    print('Automatic local sign-in enabled; the credential remains server-side.')
    print(f'Manual observer credential: {root / "credentials/viewer.json"}')
    print(f'Data: {database}')
    print(f'Owner expiry: {local.expires_at(owner).isoformat()}; rotate before expiry with --rotate-credentials.')
    print('Both services start at login and restart automatically after exit.')
