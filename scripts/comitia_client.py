#!/usr/bin/env python3
"""Run the installed Comitia binary as one local identity.

  comitia-client NAME COMMAND [FLAGS]
  comitia-client --identity NAME COMMAND [FLAGS]
  comitia-client --credential-file PATH COMMAND [FLAGS]
  comitia-client sessions [--prune]

NAME selects credentials/NAME.json next to this installation. A credential file
must be private to the current user (no group or other permissions).
"""
import datetime
import json
import os
from pathlib import Path
import re
import stat
import sys
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent.parent
NAME = re.compile(r'^[a-z0-9][a-z0-9_-]{0,63}$')
# Self-reported instance routing passes through; credentials and endpoints never do.
PASSED = ('COMITIA_AGENT_INSTANCE', 'COMITIA_AGENT_INSTANCE_LABEL')
USAGE = 'Usage: comitia-client (NAME | --identity NAME | --credential-file PATH) COMMAND [FLAGS]'

def session_status(document, server_url):
    credential = document['credential']
    session = credential['Session']
    expiry = datetime.datetime.fromisoformat(session['ExpiresAt'].replace('Z', '+00:00'))
    if expiry <= datetime.datetime.now(datetime.timezone.utc):
        return 'expired'
    if session.get('RevokedAt'):
        return 'revoked'
    request = urllib.request.Request(server_url.rstrip('/')+'/v1/contexts/list', data=b'{"limit":1}',
                                     headers={'Authorization': 'Bearer '+credential['Token'],
                                              'Content-Type': 'application/json'})
    # Do not follow redirects with a credential, or use an inherited HTTP proxy.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, req, fp, code, msg, headers, newurl):
            return None
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    try:
        with opener.open(request, timeout=3):
            return 'active'
    except urllib.error.HTTPError as error:
        return 'revoked' if error.code == 401 else 'unknown'
    except (OSError, ValueError):
        return 'unknown'

def sessions(args, root):
    if args not in ([], ['--prune']):
        raise SystemExit('Usage: comitia-client sessions [--prune]')
    directory = root/'sessions'
    # Refuse symlinks in the directory path as well as individual files.
    for part in (directory, *directory.parents):
        if part.is_symlink():
            raise SystemExit(f'Refusing symlink: {part}')
    if not directory.exists():
        return []
    config = json.loads((root/'local-context.json').read_text())
    rows = []
    flags = os.O_RDONLY | os.O_NOFOLLOW
    fd = os.open(directory, flags | os.O_DIRECTORY)
    try:
        directory_info = os.fstat(fd)
        if directory_info.st_uid != os.getuid() or directory_info.st_mode & 0o077:
            raise SystemExit(f'Sessions directory must be private and owned by you: {directory}')
        for name in sorted(os.listdir(fd)):
            if not name.endswith('.json'):
                continue
            info = os.stat(name, dir_fd=fd, follow_symlinks=False)
            if not stat.S_ISREG(info.st_mode) or info.st_uid != os.getuid() or info.st_mode & 0o077:
                raise SystemExit(f'Refusing non-private regular credential file: {directory/name}')
            with os.fdopen(os.open(name, flags | os.O_NONBLOCK, dir_fd=fd)) as stream:
                opened = os.fstat(stream.fileno())
                if (opened.st_dev, opened.st_ino) != (info.st_dev, info.st_ino):
                    raise SystemExit(f'Credential file changed: {directory/name}')
                document = json.load(stream)
            session = document['credential']['Session']
            status = session_status(document, document.get('server_url') or config['server_url'])
            row = dict(credential_file=str(directory/name), session_id=session['ID'],
                       expires_at=session['ExpiresAt'], status=status)
            if '--prune' in args and status in ('expired', 'revoked'):
                current = os.stat(name, dir_fd=fd, follow_symlinks=False)
                if (current.st_dev, current.st_ino) != (info.st_dev, info.st_ino):
                    raise SystemExit(f'Credential file changed: {directory/name}')
                os.unlink(name, dir_fd=fd)
                row['pruned'] = True
            rows.append(row)
    finally:
        os.close(fd)
    return rows

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
    if argv and argv[0] == 'sessions':
        print(json.dumps(sessions(argv[1:], root)))
        return
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
