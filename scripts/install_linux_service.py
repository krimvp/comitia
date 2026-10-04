#!/usr/bin/env python3
"""Install Comitia as a per-user systemd service and connect local agent harnesses.

  install_linux_service.py [install] [--binary PATH] [--port 8390] [--harness NAME ...] [--no-start]
  install_linux_service.py add-harness NAME [--instructions FILE --mcp-config FILE] [...]
"""
import argparse
import getpass
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import comitia_install as local  # noqa: E402

UNIT_NAME = 'comitia.service'
UNIT = """[Unit]
Description=Comitia local agent coordination service
After=network.target

[Service]
Type=simple
ExecStart=/usr/bin/env -- {executable} --db {database} serve --listen 127.0.0.1:{port}
UnsetEnvironment=COMITIA_SERVER_URL COMITIA_CREDENTIAL COMITIA_BOOTSTRAP_CREDENTIAL
Restart=always
RestartSec=3
UMask=0077
NoNewPrivileges=true

[Install]
WantedBy=default.target
"""

def systemd_path(path):
    """Quote a literal ExecStart argument, escaping specifiers and environment expansion."""
    if any(ord(c) < 32 or ord(c) == 127 for c in str(path)):
        raise ValueError('systemd executable paths cannot contain control characters')
    value = str(path).replace('%', '%%').replace('$', '$$')
    value = value.replace('\\', '\\\\').replace('"', '\\"')
    return '"'+value+'"'

def service_unit(root, port):
    # env execs the binary; systemd rejects quotes/backslashes in executable names.
    # All daemon paths are absolute; no WorkingDirectory escaping is needed.
    return UNIT.format(executable=systemd_path(root/'bin/comitia'), database=systemd_path(root/'data/comitia.db'), port=port)

def legacy_unit(root, port):
    return UNIT.format(executable=root/'bin/comitia', database=root/'data/comitia.db', port=port).replace(
        'Type=simple\n', f'Type=simple\nWorkingDirectory={root}\n').replace('/usr/bin/env -- ', '')

def owns_unit(unit, root, port):
    if not unit.exists() or port is None:
        return False
    commands = [line for line in unit.read_text().splitlines() if line.startswith('ExecStart=')]
    current = next(line for line in service_unit(root, port).splitlines() if line.startswith('ExecStart='))
    legacy = f'ExecStart={root}/bin/comitia --db {root}/data/comitia.db serve --listen 127.0.0.1:{port}'
    return commands in ([current], [legacy])

def installation_text(root, local_context, names, linger):
    examples = names[:2] or ['NAME']
    startup = {True: "Startup before login is enabled by the account's existing systemd linger setting.",
               False: 'The service starts at login; run `loginctl enable-linger` to start it before login.',
               None: 'Startup before login depends on the systemd linger setting (`loginctl show-user`).'}[linger]
    client = root/'bin/comitia-client'
    return f"""Comitia local installation

API and inspector: {local_context['server_url']}/ui/
Shared context: {local_context['context_id']}
Data: {root/'data/comitia.db'}
Credentials: {root/'credentials'}/ (private; expires after one year)
Owner expiry: {local.expires_at(root/'credentials/owner.json').isoformat()}
Rotate before expiry: scripts/install_linux_service.py rotate-credentials
Harnesses: {root/'harnesses.json'} (add one: scripts/install_linux_service.py add-harness NAME ...)
Client commands: {client} {examples[0]} context-list
                 {client} --identity {examples[-1]} replay --context-id {local_context['context_id']}
Status: systemctl --user status {UNIT_NAME}
Restart: systemctl --user restart {UNIT_NAME}
Logs: journalctl --user -u {UNIT_NAME}
Disable: systemctl --user disable --now {UNIT_NAME}
Uninstall managed integration: scripts/install_linux_service.py uninstall (keeps data/credentials)

{startup}
Restart agent sessions to load the MCP server and communication instructions.
Existing configuration backups are stored in {root/'backups'}/.
"""

def systemctl(*command):
    subprocess.run(['systemctl', '--user', *command], check=True)

def linger_enabled():
    result = subprocess.run(['loginctl', 'show-user', getpass.getuser(), '--property=Linger', '--value'],
                            capture_output=True, text=True)
    return result.stdout.strip() == 'yes' if result.returncode == 0 else None

def default_harnesses(root, home):
    """Registered harnesses, plus built-in ones whose instruction directory exists."""
    registered = root/'harnesses.json'
    names = list(json.loads(registered.read_text())) if registered.exists() else []
    for name, spec in local.harnesses(root).items():
        if name not in names and local.expand(spec['instructions'], home).parent.is_dir():
            names.append(name)
    return names

@local.locked_installation(0)
def install(root, home, port=None, names=None, binary=None, service=systemctl, linger=None, check_port=True):
    """Install or update; service=None writes the unit but never calls systemctl."""
    unit = home/'.config/systemd/user'/UNIT_NAME
    port = local.installation_port(root, port, 8390)
    unit_text = service_unit(root, port)
    if check_port:
        owned = False
        if port == local.saved_port(root) and owns_unit(unit, root, port):
            owned = subprocess.run(['systemctl', '--user', 'is-active', '--quiet', UNIT_NAME],
                                   capture_output=True).returncode == 0
        local.preflight_port(port, owned)
    for directory in (root, root/'bin', root/'data', root/'credentials', root/'backups'):
        directory.mkdir(parents=True, exist_ok=True)
        directory.chmod(0o700)
    database = root/'data/comitia.db'
    run = local.runner(local.install_binary(root, binary), database)
    # A reinstall never bootstraps or replaces an existing database or credential;
    # it resumes an interrupted first run.
    context = local.bootstrap(run, root, database)
    local.validate_install(run, root, context['context_id'])
    path = root/'local-context.json'
    context['server_url'] = f'http://127.0.0.1:{port}'
    local.write_text(path, json.dumps(context, indent=2)+'\n', mode=0o600)
    client = root/'bin/comitia-client'
    shutil.copy2(local.REPO/'scripts/comitia_client.py', client)
    client.chmod(0o700)
    table = local.harnesses(root)
    names = default_harnesses(root, home) if names is None else names
    for name in names:
        if name not in table:
            raise RuntimeError(f'Unknown harness {name!r}; register it with add-harness first')
        local.add_harness(name, table[name], root, home, run)
    local.write_text(root/'INSTALLATION.txt', installation_text(root, context, names, linger), mode=0o600)
    local.write_text(unit, unit_text, mode=0o644)
    if service:
        service('daemon-reload')
        service('enable', UNIT_NAME)
        service('restart', UNIT_NAME)
    return context

def harness_spec(args, known):
    """Build a harness spec from add-harness flags, starting from a known entry."""
    spec = json.loads(json.dumps(known.get(args.name, {})))
    if args.display:
        spec['display'] = args.display
    if args.instructions:
        spec['instructions'] = str(args.instructions.expanduser().absolute())
    if args.mcp_config:
        fmt = args.mcp_format or ('toml' if args.mcp_config.suffix == '.toml' else 'json')
        spec['mcp'] = {'file': str(args.mcp_config.expanduser().absolute()), 'format': fmt,
                       'key': args.mcp_key or ('mcp_servers' if fmt == 'toml' else 'mcpServers')}
    if args.approve_entry:
        key, _, value = args.approve_entry.partition('=')
        try:
            value = json.loads(value)
        except ValueError:
            pass
        spec['approval'] = {'format': 'entry', 'key': key, 'value': value}
    if args.approve_list:
        file, key, value = args.approve_list
        spec['approval'] = {'format': 'list', 'file': str(Path(file).expanduser().absolute()), 'key': key, 'value': value}
    if args.no_approval:
        spec.pop('approval', None)
    return spec

@local.locked_installation(0)
def uninstall(root, home, service=systemctl):
    unit = home/'.config/systemd/user'/UNIT_NAME
    retained = []
    port = local.saved_port(root)
    if owns_unit(unit, root, port):
        if service:
            service('disable', '--now', UNIT_NAME)
        # Keep a customized unit for manual review, even after stopping it.
        if unit.read_text() in (service_unit(root, port), legacy_unit(root, port)):
            unit.unlink()
            if service:
                service('daemon-reload')
        else:
            retained.append(str(unit))
    elif unit.exists():
        retained.append(str(unit))
    retained.extend(local.remove_harnesses(root, home))
    return retained

def parser():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    commands = parser.add_subparsers(dest='command')
    commands.add_parser('uninstall', help='Stop/disable and remove managed integration; keep data and credentials')
    setup = commands.add_parser('install', help='Install or update the service and harnesses')
    setup.add_argument('--binary', type=Path, help='Use an already built Comitia binary')
    setup.add_argument('--port', type=int, help='Listen port (default: saved port, or 8390 on first install)')
    setup.add_argument('--harness', action='append', help='Harness to connect (repeatable); default: registered '
                       'harnesses plus built-in ones whose config directory exists')
    setup.add_argument('--no-start', action='store_true', help='Write the unit without calling systemctl')
    add = commands.add_parser('add-harness', help='Connect one harness: credential, MCP server, auto-approval, instructions')
    add.add_argument('name')
    add.add_argument('--instructions', type=Path, help='Instruction file receiving the managed block')
    add.add_argument('--mcp-config', type=Path, help='MCP config file registering the comitia server')
    add.add_argument('--mcp-format', choices=('toml', 'json'), help='Default: from the file extension')
    add.add_argument('--mcp-key', help='Table/key holding MCP servers (mcp_servers for toml, mcpServers for json)')
    add.add_argument('--display', help='Name used in the instructions, e.g. "Factory Droid"')
    add.add_argument('--approve-entry', metavar='KEY=VALUE', help='Auto-approval key set in the server entry')
    add.add_argument('--approve-list', nargs=3, metavar=('FILE', 'KEY', 'VALUE'),
                     help='Auto-approval value appended to the JSON list at dotted KEY in FILE')
    add.add_argument('--no-approval', action='store_true', help='Do not configure auto-approval')
    add.add_argument('--permissions', default='read,write,claim,grant')
    rotate = commands.add_parser('rotate-credentials', help='Replace owner and local harness credentials before expiry')
    rotate.add_argument('--owner-credential', type=Path, help='Another active admin credential if the installed owner is unusable')
    rotate.add_argument('--revoke-old', action='store_true', help='Revoke old sessions and their descendants after publishing replacements')
    return parser

def main(argv):
    if not argv or argv[0].startswith('-') and argv[0] not in ('-h', '--help'):
        argv = ['install', *argv]
    args = parser().parse_args(argv)
    home = Path.home()
    root = home/'.local/share/comitia'
    if args.command == 'uninstall':
        retained = uninstall(root, home, systemctl)
        print(f'Removed managed integration. Data, credentials and backups remain in {root}. Restart agent sessions.')
        if retained:
            print('Retained changed/unrelated entries for manual review: '+', '.join(retained))
        return
    if args.command == 'install':
        if not sys.platform.startswith('linux'):
            raise SystemExit('This installer requires Linux with systemd; on macOS use install_macos_service.py.')
        if args.port is not None and not 1024 <= args.port <= 65535:
            raise SystemExit('Choose a port between 1024 and 65535.')
        context = install(root, home, args.port, args.harness, args.binary,
                          None if args.no_start else systemctl, None if args.no_start else linger_enabled(),
                          check_port=True)
        print((root/'INSTALLATION.txt').read_text(), end='')
        return context
    if not (root/'local-context.json').exists():
        raise SystemExit(f'No installation in {root}; run the installer first.')
    if args.command == 'rotate-credentials':
        archive = local.rotate_credentials(root, local.runner(root/'bin/comitia', root/'data/comitia.db'),
                                           args.owner_credential, args.revoke_old)
        print(f'Rotated local credentials. Originals: {archive}. Restart agent sessions. '
              'Old sessions remain valid until expiry unless --revoke-old was used.')
        return
    spec = harness_spec(args, local.harnesses(root))
    run = local.runner(root/'bin/comitia', root/'data/comitia.db')
    local.add_harness(args.name, spec, root, home, run, args.permissions)
    print(f'Connected {args.name}: credential {root/"credentials"/(args.name+".json")}, '
          f'instructions {local.expand(spec["instructions"], home)}. Restart its sessions to load Comitia.')

if __name__ == '__main__':
    try:
        main(sys.argv[1:])
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.CalledProcessError) as error:
        raise SystemExit(f'error: {error}')
