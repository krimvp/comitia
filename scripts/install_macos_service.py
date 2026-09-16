#!/usr/bin/env python3
"""Install Comitia and its world viewer as per-user macOS launchd services."""
import argparse
import json
import os
from pathlib import Path
import plistlib
import secrets
import shutil
import socket
import subprocess
import sys

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--binary', type=Path, help='Use an already built Comitia binary')
parser.add_argument('--port', type=int, default=8080)
parser.add_argument('--viewer-port', type=int, default=4174)
args = parser.parse_args()
if sys.platform != 'darwin':
    parser.error('This installer requires macOS.')
if args.port == args.viewer_port or any(not 1024 <= p <= 65535 for p in (args.port, args.viewer_port)):
    parser.error('Choose distinct ports between 1024 and 65535.')
repo = Path(__file__).resolve().parent.parent
root = Path.home() / 'Library/Application Support/Comitia'
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
binary = root/'bin/comitia'
staged = root/'bin/comitia.new'
if args.binary:
    shutil.copy2(args.binary.resolve(), staged)
else:
    subprocess.run(['go', 'build', '-o', str(staged), './cmd/comitia'], cwd=repo, check=True)
staged.chmod(0o700)
os.replace(staged, binary)
# Exclude inherited credentials and server routing from local initialization.
env = {k:v for k,v in os.environ.items() if not k.startswith('COMITIA_')}
database = root/'data/comitia.db'
env['COMITIA_DB'] = str(database)
def run(*command, token=None):
    command_env = dict(env)
    if token:
        command_env['COMITIA_CREDENTIAL'] = token
    result = subprocess.run([str(binary), *command], env=command_env, capture_output=True, text=True)
    if result.returncode:
        raise RuntimeError(f'Comitia {command[0]} failed: {result.stderr.strip()}')
    return json.loads(result.stdout)
def private_json(path, value):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(value, stream, indent=2)
        stream.write('\n')
if not database.exists():
    env['COMITIA_BOOTSTRAP_CREDENTIAL'] = secrets.token_urlsafe(32)
    owner = run('bootstrap', '--name', 'Local owner', '--ttl', '8760h')
    private_json(root/'credentials/owner.json', owner)
    env.pop('COMITIA_BOOTSTRAP_CREDENTIAL', None)
    token = owner['credential']['Token']
    workspace = run('workspace-create', '--name', 'Local workspace', token=token)
    context = run('context-create', '--workspace-id', workspace['ID'], '--name', 'Local agents', token=token)
    observer = run('delegate', '--name', 'World viewer', '--ttl', '744h', '--context-id', context['ID'],
                   '--permissions', 'read', '--grant-ttl', '720h', token=token)
    private_json(root/'credentials/viewer.json', observer)
    private_json(root/'local-context.json', {'workspace_id': workspace['ID'], 'context_id': context['ID']})
    database.chmod(0o600)
# A reinstall never bootstraps or replaces an existing database or credential.
viewer = root/'viewer'
shutil.copytree(repo/'httpapi/ui/world-prototype', viewer, dirs_exist_ok=True,
                ignore=shutil.ignore_patterns('__pycache__', '*.pyc'))
configs = [
    {'Label': labels[0], 'ProgramArguments': [str(binary), '--db', str(database), 'serve', '--listen', f'127.0.0.1:{args.port}']},
    {'Label': labels[1], 'ProgramArguments': ['/usr/bin/python3', str(viewer/'serve.py'), '--port', str(args.viewer_port), '--upstream', f'http://127.0.0.1:{args.port}']}
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
print(f'Private viewer credential: {root / "credentials/viewer.json"}')
print(f'Data: {database}')
print('Both services start at login and restart automatically after exit.')
