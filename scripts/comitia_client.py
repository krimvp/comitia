#!/usr/bin/env python3
"""Run the installed Comitia binary as one local identity.

  comitia-client NAME COMMAND [FLAGS]
  comitia-client --identity NAME COMMAND [FLAGS]
  comitia-client --credential-file PATH COMMAND [FLAGS]

NAME selects credentials/NAME.json next to this installation. A credential file
must be private to the current user (no group or other permissions).
"""
import json
import os
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parent.parent
NAME = re.compile(r'^[a-z0-9][a-z0-9_-]{0,63}$')
# Self-reported instance routing passes through; credentials and endpoints never do.
PASSED = ('COMITIA_AGENT_INSTANCE', 'COMITIA_AGENT_INSTANCE_LABEL')
USAGE = 'Usage: comitia-client (NAME | --identity NAME | --credential-file PATH) COMMAND [FLAGS]'

def option(args, flag):
    """Return (value, rest) when args start with --flag VALUE or --flag=VALUE."""
    if args[0] == flag:
        if len(args) < 2: raise SystemExit(USAGE)
        return args[1], args[2:]
    if args[0].startswith(flag+'='):
        return args[0].split('=', 1)[1], args[1:]
    return None, args

def select(args, root=ROOT):
    """Return (credential path, remaining args) for the wrapper's leading selector."""
    if not args: raise SystemExit(USAGE)
    path, rest = option(args, '--credential-file')
    if path is not None:
        return Path(path).expanduser(), rest
    name, rest = option(args, '--identity')
    if name is None:
        if args[0].startswith('-'): raise SystemExit(USAGE)
        name, rest = args[0], args[1:]
    # The owner credential administers the installation; agents never run as it.
    if not NAME.match(name) or name == 'owner':
        raise SystemExit(f'Invalid identity {name!r}')
    path = root/'credentials'/f'{name}.json'
    if not path.exists():
        known = sorted(p.stem for p in (root/'credentials').glob('*.json') if p.stem != 'owner')
        raise SystemExit(f'Unknown identity {name!r}; installed: {", ".join(known) or "none"}')
    return path, rest

def load(path):
    info = path.stat()
    if info.st_uid != os.getuid() or info.st_mode & 0o077:
        raise SystemExit(f'Credential file must be owned by you with mode 0600: {path}')
    return json.loads(path.read_text())

def main(argv, root=ROOT):
    path, args = select(argv, root)
    document = load(path)
    config = json.loads((root/'local-context.json').read_text())
    env = {k:v for k,v in os.environ.items() if not k.startswith('COMITIA_') or k in PASSED}
    env.update(COMITIA_SERVER_URL=document.get('server_url') or config['server_url'],
               COMITIA_CREDENTIAL=document.get('credential', document)['Token'])
    binary = str(root/'bin/comitia')
    os.execve(binary, [binary, *args], env)

if __name__ == '__main__':
    main(sys.argv[1:])
