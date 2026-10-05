#!/usr/bin/env python3
"""Install Comitia and its world viewer as per-user macOS launchd services."""
import argparse
import datetime
import json
import os
from pathlib import Path
import plistlib
import re
import secrets
import shutil
import shlex
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import comitia_install as local  # noqa: E402
import install_linux_service as harness_install  # noqa: E402

LABELS = ('com.comitia.server', 'com.comitia.viewer')

def service_configs(root, port, viewer_port):
    configs = [
        {'Label': LABELS[0], 'ProgramArguments': [str(root/'bin/comitia'), '--db', str(root/'data/comitia.db'), 'serve', '--listen', f'127.0.0.1:{port}'],
         'EnvironmentVariables': dict.fromkeys(('COMITIA_SERVER_URL', 'COMITIA_CREDENTIAL', 'COMITIA_BOOTSTRAP_CREDENTIAL'), '')},
        {'Label': LABELS[1], 'ProgramArguments': ['/usr/bin/python3', str(root/'viewer/serve.py'), '--port', str(viewer_port), '--upstream', f'http://127.0.0.1:{port}', '--credential-file', str(root/'credentials/viewer.json'), '--launch-key-file', str(root/'credentials/viewer-launch-key')]}
    ]
    for config in configs:
        label = config['Label']
        config.update(RunAtLoad=True, KeepAlive=True, ThrottleInterval=5, WorkingDirectory=str(root),
                      Umask=0o077, StandardOutPath=str(root/'logs'/f'{label}.out.log'),
                      StandardErrorPath=str(root/'logs'/f'{label}.err.log'))
    return configs

def managed_links(root):
    client = root/'bin/comitia-local'
    return {'comitia': client, 'comitia-local': client, 'comitia-viewer': root/'bin/comitia-viewer'}

def owns_service(path, root, label, port):
    if not path.exists() or port is None:
        return False
    config = plistlib.loads(path.read_bytes())
    return owns_arguments(config.get('ProgramArguments', []), root, label, port)

def owns_arguments(args, root, label, port):
    if label == 'com.comitia.server':
        return args == [str(root/'bin/comitia'), '--db', str(root/'data/comitia.db'),
                        'serve', '--listen', f'127.0.0.1:{port}']
    return (len(args) > 4 and args[:2] == ['/usr/bin/python3', str(root/'viewer/serve.py')]
            and args[2:4] == ['--port', str(port)])

def running_service(root, label, port):
    result = subprocess.run(['launchctl', 'print', f'gui/{os.getuid()}/{label}'], capture_output=True, text=True)
    if result.returncode or not re.search(r'^\s*state = running\s*$', result.stdout, re.M):
        return False
    pid = re.search(r'^\s*pid = ([1-9][0-9]*)\s*$', result.stdout, re.M)
    arguments = re.search(r'^\s*arguments = \{\n(.*?)^\s*\}', result.stdout, re.M | re.S)
    # Read the loaded job, not the plist rewritten by an interrupted installation.
    return bool(pid and arguments and owns_arguments(
        [line.strip() for line in arguments[1].splitlines()], root, label, port))

@local.locked_installation(0)
def uninstall(root, home):
    retained = []
    domain = f'gui/{os.getuid()}'
    port, viewer_port = local.saved_port(root), local.saved_port(root, 'viewer_url')
    for config, service_port in zip(service_configs(root, port, viewer_port), (port, viewer_port)):
        label = config['Label']
        path = home/'Library/LaunchAgents'/f'{label}.plist'
        if not path.exists():
            continue
        if owns_service(path, root, label, service_port):
            subprocess.run(['launchctl', 'disable', f'{domain}/{label}'], check=True)
            loaded = subprocess.run(['launchctl', 'print', f'{domain}/{label}'], capture_output=True).returncode == 0
            if loaded:
                subprocess.run(['launchctl', 'bootout', f'{domain}/{label}'], check=True)
            current = plistlib.loads(path.read_bytes())
            forms = [config, {k: v for k, v in config.items() if k != 'EnvironmentVariables'}]
            if label == LABELS[1]:
                # Viewer plists before the scoped launcher used the owner credential.
                arguments = config['ProgramArguments'][:-4] + ['--credential-file', str(root/'credentials/owner.json')]
                forms.append({**config, 'ProgramArguments': arguments})
            if current in forms:
                path.unlink()
            else:
                retained.append(str(path))
        else:
            retained.append(str(path))
    for name, target in managed_links(root).items():
        link = home/'.local/bin'/name
        if link.is_symlink() and link.resolve() == target.resolve():
            link.unlink()
        elif link.exists() or link.is_symlink():
            retained.append(str(link))
    retained.extend(local.remove_harnesses(root, home))
    return retained

def main(argv):
    if argv and argv[0] == 'add-harness':
        if sys.platform != 'darwin':
            raise SystemExit('This installer requires macOS.')
        args = harness_install.parser().parse_args(argv)
        root = Path.home()/'Library/Application Support/Comitia'
        if not (root/'local-context.json').exists():
            raise SystemExit(f'No installation in {root}; run the installer first.')
        return harness_install.connect_harness(args, root, Path.home())
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--binary', type=Path, help='Use an already built Comitia binary')
    parser.add_argument('--port', type=int, help='Server port (default: saved port, or 8080)')
    parser.add_argument('--viewer-port', type=int, help='Viewer port (default: saved port, or 4174)')
    actions = parser.add_mutually_exclusive_group()
    actions.add_argument('--uninstall', action='store_true', help='Stop/disable and remove managed integration; keep data and credentials')
    actions.add_argument('--rotate-credentials', action='store_true', help='Replace owner and local harness/viewer credentials before expiry')
    parser.add_argument('--owner-credential', type=Path, help='Another active admin credential for rotation')
    parser.add_argument('--revoke-old', action='store_true', help='Revoke old sessions and descendants after rotation')
    args = parser.parse_args(argv)
    if sys.platform != 'darwin':
        parser.error('This installer requires macOS.')
    repo = Path(__file__).resolve().parent.parent
    root = Path.home() / 'Library/Application Support/Comitia'
    if args.uninstall:
        retained = uninstall(root, Path.home())
        print(f'Removed managed integration. Data, credentials and backups remain in {root}. Restart agent sessions.')
        if retained:
            print('Retained changed/unrelated entries for manual review: '+', '.join(retained))
        return
    if args.rotate_credentials:
        archive = local.rotate_credentials(root, local.runner(root/'bin/comitia', root/'data/comitia.db'),
                                           args.owner_credential, args.revoke_old)
        print(f'Rotated local credentials. Originals: {archive}. Restart agent sessions and the viewer service.')
        raise SystemExit(0)
    if args.owner_credential or args.revoke_old:
        parser.error('--owner-credential and --revoke-old require --rotate-credentials')
    with local.installation_lock(root):
        args.port = local.installation_port(root, args.port, 8080)
        args.viewer_port = local.installation_port(root, args.viewer_port, 4174, 'viewer_url')
        if args.port == args.viewer_port:
            parser.error('Choose distinct ports between 1024 and 65535.')
        launch_dir = Path.home() / 'Library/LaunchAgents'
        labels = LABELS
        domain = f'gui/{os.getuid()}'
        for port, label, key in zip((args.port, args.viewer_port), labels, ('server_url', 'viewer_url')):
            owned = False
            if port == local.saved_port(root, key) and owns_service(launch_dir/f'{label}.plist', root, label, port):
                owned = running_service(root, label, port)
            local.preflight_port(port, owned)
        local_bin = Path.home()/'.local/bin'
        for name, target in managed_links(root).items():
            link = local_bin/name
            if (link.exists() or link.is_symlink()) and not (link.is_symlink() and link.resolve() == target.resolve()):
                raise RuntimeError(f'Refusing to replace an unrelated command: {link}')
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
        wrapper = root/'bin/comitia-client'
        shutil.copy2(repo/'scripts/comitia_client.py', wrapper)
        wrapper.chmod(0o700)
        local.install_skill(root, root/'skills/comitia')
        table = local.harnesses(root)
        names = harness_install.default_harnesses(root, Path.home())
        for name in names:
            local.add_harness(name, table[name], root, Path.home(), run)
        owner = root/'credentials/owner.json'
        if not (root/'credentials/viewer.json').exists():
            observer = run('delegate', '--name', 'World viewer', '--ttl', local.child_ttl(owner, datetime.timedelta(hours=744)),
                           '--context-id', context['context_id'], '--permissions', 'read',
                           '--grant-ttl', local.child_ttl(owner, datetime.timedelta(hours=720)), token=local.token(owner))
            local.private_json(root/'credentials/viewer.json', observer)
        launcher_key = root/'credentials/viewer-launch-key'
        if not launcher_key.exists():
            descriptor = os.open(launcher_key, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
            with os.fdopen(descriptor, 'w') as stream:
                stream.write(secrets.token_urlsafe(32) + '\n')
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
        viewer_launcher = root/'bin/comitia-viewer'
        viewer_launcher.write_text('#!/bin/sh\nexec /usr/bin/python3 ' + shlex.quote(str(viewer/'launch.py')) + '\n')
        viewer_launcher.chmod(0o700)
        link = local_bin/'comitia-viewer'
        if link.is_symlink() and link.resolve() == viewer_launcher:
            pass
        elif link.exists() or link.is_symlink():
            raise RuntimeError(f'Refusing to replace an unrelated command: {link}')
        else:
            link.symlink_to(viewer_launcher)
        for config in service_configs(root, args.port, args.viewer_port):
            label = config['Label']
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
        print('Open with comitia-viewer; launcher authentication keeps the read-only credential server-side.')
        print(f'Manual observer credential: {root / "credentials/viewer.json"}')
        print(f'Data: {database}')
        print(f'Owner expiry: {local.expires_at(owner).isoformat()}; rotate before expiry with --rotate-credentials.')
        print('Both services start at login and restart automatically after exit.')
        print('Connected harnesses: '+(', '.join(names) or 'none')+'. Restart their sessions to load Comitia.')

if __name__ == '__main__':
    try:
        main(sys.argv[1:])
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.CalledProcessError) as error:
        raise SystemExit(f'error: {error}')
