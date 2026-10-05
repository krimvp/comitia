"""Shared steps of the local installers: binary, owner bootstrap and harness registration.

Every function takes the installation root and home directory explicitly, so
the installers and their tests never depend on the caller's real home.
"""
import datetime
import contextlib
import copy
import fcntl
import functools
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import socket
import string
import sys
import subprocess
import tempfile
import threading
import urllib.request
import urllib.parse
try:
    import tomllib
except ImportError:  # Python < 3.11 edits without the final parse check.
    tomllib = None

REPO = Path(__file__).resolve().parent.parent
LOCAL = REPO/'scripts/local'
NAME = re.compile(r'^[a-z0-9][a-z0-9_-]{0,63}$')
BLOCK = re.compile(r'<!-- comitia-local:start(?: v\d+)? -->.*?<!-- comitia-local:end -->\n?', re.S)
TOML_KEY = re.compile(r'''^\s*([A-Za-z0-9_-]+|'[^']*'|"(?:[^"\\]|\\.)*")\s*=''')
ONE_YEAR = datetime.timedelta(hours=8760)
# Children must expire before their parent; the owner outlives a full-year child by this margin.
MARGIN = datetime.timedelta(hours=1)
SERVER = 'comitia'
SKILL_VERSION = 1
PERMISSIONS = ('read', 'write', 'claim', 'grant', 'admin', 'create')
RECOVERY = ('Run rotate-credentials before owner expiry (macOS: --rotate-credentials), or use '
            '--owner-credential with another active admin. If all admin sessions have expired, '
            'API recovery is unavailable; preserve the database and restore a backup with a live admin. '
            'Reinstall cannot bootstrap an existing database.')
INSTALL_LOCKS = threading.local()

@contextlib.contextmanager
def installation_lock(root):
    """Exclude other installer processes/threads; allow nested calls in this thread."""
    root = Path(root).resolve()
    held = getattr(INSTALL_LOCKS, 'roots', set())
    if root in held:
        yield
        return
    root.mkdir(parents=True, exist_ok=True, mode=0o700)
    fd = os.open(root/'.install.lock', os.O_RDWR | os.O_CREAT, 0o600)
    try:
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as error:
            raise RuntimeError(f'Installation {root} is busy: another installer is running; retry after it completes.') from error
        held.add(root)
        INSTALL_LOCKS.roots = held
        try:
            yield
        finally:
            held.remove(root)
    finally:
        os.close(fd)

def locked_installation(root_index):
    def decorate(function):
        @functools.wraps(function)
        def invoke(*args, **kwargs):
            root = args[root_index] if len(args) > root_index else kwargs['root']
            with installation_lock(root):
                return function(*args, **kwargs)
        return invoke
    return decorate

def expand(path, home):
    """Expand a leading ~ against home rather than the process's HOME."""
    path = str(path)
    if path == '~' or path.startswith('~/'):
        return Path(home)/path[2:]
    return Path(path)

def saved_port(root, key='server_url'):
    for name in ('service.json', 'local-context.json'):
        path = root/name
        if path.exists():
            url = json.loads(path.read_text()).get(key)
            if url:
                port = urllib.parse.urlsplit(url).port
                if port is None or not 1024 <= port <= 65535:
                    raise ValueError(f'Invalid saved {key} in {path}')
                return port
    return None

def installation_port(root, port, default, key='server_url'):
    port = port if port is not None else saved_port(root, key) or default
    if not 1024 <= port <= 65535:
        raise ValueError('Choose a port between 1024 and 65535.')
    return port

def preflight_port(port, owned=False):
    if owned:
        return
    with socket.socket() as probe:
        try:
            probe.bind(('127.0.0.1', port))
        except OSError as error:
            raise RuntimeError(f'Port {port} is already occupied. Choose another port.') from error

def private_json(path, value):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(value, stream, indent=2)
        stream.write('\n')

def write_text(path, text, backups=None, mode=None):
    """Atomically replace path, keeping its mode; the first changed original goes to backups."""
    path = Path(path)
    old = path.read_text() if path.exists() else None
    if old == text:
        return False
    path.parent.mkdir(parents=True, exist_ok=True)
    if mode is None:
        mode = path.stat().st_mode & 0o777 if old is not None else 0o600
    if old is not None and backups:
        backups.mkdir(mode=0o700, parents=True, exist_ok=True)
        stamp = datetime.datetime.now().strftime('%Y%m%d%H%M%S')
        backup = backups/f'{"_".join(path.parts[-2:]).lstrip(".")}.{stamp}'
        if not backup.exists():
            shutil.copy2(path, backup)
    staged = path.with_name(f'.{path.name}.comitia-new')
    fd = os.open(staged, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, mode)
    with os.fdopen(fd, 'w') as stream:
        stream.write(text)
    staged.chmod(mode)
    os.replace(staged, path)
    return True

def token(path):
    document = json.loads(Path(path).read_text())
    return document.get('credential', document)['Token']

def build_flags():
    revision, dirty = 'unknown', 'unknown'
    try:
        revision = subprocess.run(['git', 'rev-parse', 'HEAD'], cwd=REPO, check=True,
                                  capture_output=True, text=True).stdout.strip()
        status = subprocess.run(['git', 'status', '--porcelain', '--untracked-files=all'], cwd=REPO,
                                check=True, capture_output=True, text=True).stdout
        dirty = 'true' if status else 'false'
    except (OSError, subprocess.CalledProcessError):
        pass
    return f'-X github.com/krimvp/comitia.Revision={revision} -X github.com/krimvp/comitia.Dirty={dirty}'

def install_binary(root, source=None):
    """Install bin/comitia from source, or build it from this checkout."""
    binary = root/'bin/comitia'
    staged = root/'bin/comitia.new'
    if source:
        shutil.copy2(Path(source).resolve(), staged)
    else:
        subprocess.run(['go', 'build', '-ldflags', build_flags(), '-o', str(staged), './cmd/comitia'],
                       cwd=REPO, check=True)
    staged.chmod(0o700)
    os.replace(staged, binary)
    report_stale_mcp_clients(binary)
    return binary

def stale_mcp_clients(binary, proc_root=Path('/proc')):
    """Find MCP stdio processes using an older copy of this installed binary.

    Linux /proc/exe still references the old inode after atomic replacement.
    Processes can disappear or deny access mid-scan; never inspect or report
    their environment or full arguments (which may contain credentials).
    """
    if not sys.platform.startswith('linux'):
        return []
    try:
        binary = Path(binary).resolve()
        installed = binary.stat()
        entries = list(Path(proc_root).iterdir())
    except OSError:
        return []
    stale = []
    for process in entries:
        if not process.name.isdigit():
            continue
        try:
            target = os.readlink(process/'exe').removesuffix(' (deleted)')
            if target != str(binary):
                continue
            argv = (process/'cmdline').read_bytes().split(b'\0')
            args = iter(argv[1:])
            command = None
            for arg in args:
                if arg.split(b'=', 1)[0] in (b'--db', b'--server-url', b'--credential', b'--bootstrap-credential'):
                    if b'=' not in arg:
                        next(args, None)
                else:
                    command = arg
                    break
            if command != b'mcp':
                continue
            running = (process/'exe').stat()
            identity = lambda stat: (stat.st_dev, stat.st_ino, stat.st_mtime_ns)
            if identity(running) != identity(installed):
                stale.append(int(process.name))
        except (OSError, ValueError):
            continue
    return sorted(stale)

def report_stale_mcp_clients(binary, proc_root=Path('/proc')):
    pids = stale_mcp_clients(binary, proc_root)
    if pids:
        print('Running comitia mcp clients still use the old binary (PIDs: '+
              ', '.join(map(str, pids))+'). Restart their agent sessions to load the upgrade.')
    return pids

def runner(binary, database):
    """Return run(*command, token=None) against the embedded database."""
    # Exclude inherited credentials and server routing from local administration.
    base = {k:v for k,v in os.environ.items() if not k.startswith('COMITIA_')}
    base['COMITIA_DB'] = str(database)
    def run(*command, token=None, bootstrap=None, database=None):
        env = dict(base)
        if database:
            env['COMITIA_DB'] = str(database)
        if token:
            env['COMITIA_CREDENTIAL'] = token
        if bootstrap:
            env['COMITIA_BOOTSTRAP_CREDENTIAL'] = bootstrap
        result = subprocess.run([str(binary), *command], env=env, capture_output=True, text=True)
        if result.returncode:
            raise RuntimeError(f'Comitia {command[0]} failed: {result.stderr.strip()}')
        return json.loads(result.stdout)
    return run

def duration(delta):
    return f'{int(delta.total_seconds())}s'

def expires_at(path):
    """Return the session expiry recorded in a credential file."""
    value = json.loads(Path(path).read_text())['credential']['Session']['ExpiresAt']
    # Go writes RFC 3339 with up to nanoseconds; fromisoformat takes microseconds.
    value = re.sub(r'(\.\d{6})\d+', r'\1', value).replace('Z', '+00:00')
    return datetime.datetime.fromisoformat(value)

def child_ttl(owner, limit=ONE_YEAR):
    """Return a delegate --ttl that ends at least MARGIN before the owner session does."""
    left = expires_at(owner) - datetime.datetime.now(datetime.timezone.utc) - MARGIN
    if left <= datetime.timedelta(minutes=1):
        raise RuntimeError(f'The owner credential {owner} expires at {expires_at(owner)}; renew it before adding harnesses.')
    return duration(min(limit, left))

def validate_credential(run, path, context_id=None):
    """Check both the saved expiry and the authoritative session/grant ancestry."""
    try:
        if expires_at(path) <= datetime.datetime.now(datetime.timezone.utc):
            raise RuntimeError('expired')
        document = json.loads(Path(path).read_text())
        grants = document.get('grants', [])
        if Path(path).stem != 'owner' and grants:
            for grant in grants:
                permissions = ','.join(p for i, p in enumerate(PERMISSIONS) if grant['Permissions'] & (1 << i))
                try:
                    run('credential-check', '--context-id', grant['ContextID'], '--permissions', permissions, token=token(path))
                except RuntimeError as error:
                    # Binaries installed before credential-check: check the session and context access they can.
                    if 'unknown command' not in str(error) or 'credential-check' not in str(error):
                        raise
                    run('participants', '--context-id', grant['ContextID'], token=token(path))
        elif context_id and Path(path).stem == 'owner':
            run('participants', '--context-id', context_id, token=token(path))
        else:
            run('context-list', '--limit', '1', token=token(path))
    except (OSError, ValueError, TypeError, KeyError, RuntimeError) as error:
        raise RuntimeError(f'Credential {Path(path).stem} at {path} is unusable '
                           f'(expired, revoked, invalid, or missing context access). {RECOVERY}') from error

def validate_install(run, root, context_id):
    for path in sorted((root/'credentials').glob('*.json')):
        if path.name != 'owner.json':
            validate_credential(run, path, context_id)

def mint_owner(server_url, document):
    """Use the existing admin-only REST mint; delegation cannot outlive its parent."""
    credential = document['credential']
    request = urllib.request.Request(server_url.rstrip('/')+'/v1/sessions/mint',
        data=json.dumps({'principal_id': credential['Session']['PrincipalID'],
                         'ttl': int((ONE_YEAR+MARGIN).total_seconds())*10**9}).encode(),
        headers={'Authorization': 'Bearer '+credential['Token'], 'Content-Type': 'application/json'})
    # Local administration must not send credentials to a proxy or a redirected host.
    class NoRedirect(urllib.request.HTTPRedirectHandler):
        def redirect_request(self, *args, **kwargs):
            return None
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())
    try:
        with opener.open(request, timeout=10) as response:
            result = json.load(response)
    except (OSError, ValueError) as error:
        raise RuntimeError('Owner mint failed. Start the installed Comitia service and check that '
                           'local-context.json points to it; an active admin is required. '+RECOVERY) from error
    replacement = result['credential']
    if replacement['Session'].get('ParentID') or replacement['Session']['PrincipalID'] != credential['Session']['PrincipalID']:
        raise RuntimeError('Owner mint did not return an independent session for the admin principal')
    return {**document, 'credential': replacement, 'grants': []}

@locked_installation(0)
def rotate_credentials(root, run, owner_credential=None, revoke_old=False):
    """Stage and verify independent owner/grants and fresh local harness ancestry."""
    context = json.loads((root/'local-context.json').read_text())
    owner = root/'credentials/owner.json'
    source = Path(owner_credential) if owner_credential else owner
    validate_credential(run, source)
    old = {p.name: json.loads(p.read_text()) for p in sorted((root/'credentials').glob('*.json'))}
    scopes = {}
    for name, document in old.items():
        if name == 'owner.json':
            continue
        grants = document.get('grants', [])
        if not grants or any(g['ContextID'] != context['context_id'] for g in grants):
            raise RuntimeError(f'{name} has non-local or missing grants; rotate that credential manually')
        bits = 0
        for grant in grants:
            bits |= grant['Permissions']
        scopes[name] = ','.join(p for i, p in enumerate(PERMISSIONS) if bits & (1 << i))
    contexts = {context['context_id']}
    skipped = {}
    cursor = ''
    while True:
        page = run('context-list', '--limit', '1000', '--after-id', cursor, token=token(source))
        for c in page:
            if c.get('Archived'):
                skipped[c['ID']] = 'archived'
            else:
                contexts.add(c['ID'])
        if len(page) < 1000:
            break
        cursor = page[-1]['ID']
    if context['context_id'] in skipped:
        raise RuntimeError('The shared installation context is archived; credentials cannot be rotated for active harnesses.')
    # Older macOS installs recorded the daemon URL only in service.json.
    server_url = context.get('server_url') or json.loads((root/'service.json').read_text())['server_url']
    replacement = mint_owner(server_url, json.loads(source.read_text()))
    publishing = False
    try:
        with tempfile.TemporaryDirectory(prefix='.rotation-', dir=root/'credentials') as staging:
            staging = Path(staging)
            staged_owner = staging/'owner.json'
            private_json(staged_owner, replacement)
            # Confirm the daemon minted into this installation's database before granting access.
            validate_credential(run, staged_owner)
            # Root grants outlive full-year harness grants, while ending before the owner.
            ttl = duration(expires_at(staged_owner)-datetime.datetime.now(datetime.timezone.utc)-datetime.timedelta(minutes=1))
            for context_id in sorted(contexts):
                try:
                    grant = run('grant', '--session-id', replacement['credential']['Session']['ID'],
                        '--context-id', context_id, '--permissions', ','.join(PERMISSIONS), '--ttl', ttl,
                        token=token(staged_owner))
                except RuntimeError:
                    if context_id == context['context_id']:
                        raise
                    skipped[context_id] = 'grant could not be issued'
                    continue
                replacement['grants'].append(grant)
            staged_owner.write_text(json.dumps(replacement, indent=2)+'\n')
            validate_credential(run, staged_owner, context['context_id'])
            for name, permissions in scopes.items():
                ttl = child_ttl(staged_owner)
                document = run('delegate', '--name', Path(name).stem, '--ttl', ttl,
                    '--context-id', context['context_id'], '--permissions', permissions, '--grant-ttl', ttl,
                    token=token(staged_owner))
                private_json(staging/name, document)
                validate_credential(run, staging/name, context['context_id'])
            # Preserve originals before publishing; each credential replacement is atomic.
            (root/'backups').mkdir(parents=True, exist_ok=True, mode=0o700)
            archive = root/'backups'/('credentials-'+secrets.token_hex(8))
            archive.mkdir(mode=0o700)
            for name in old:
                shutil.copy2(root/'credentials'/name, archive/name)
                (archive/name).chmod(0o600)
            publishing = True
            for name in ['owner.json']+sorted(old.keys()-{'owner.json'}):
                os.replace(staging/name, root/'credentials'/name)
    except Exception:
        if not publishing:
            try:
                run('revoke', '--session-id', replacement['credential']['Session']['ID'], token=token(source))
            except RuntimeError:
                pass
        raise
    if revoke_old:
        for document in old.values():
            run('revoke', '--session-id', document['credential']['Session']['ID'], token=token(owner))
    installation = root/'INSTALLATION.txt'
    if installation.exists():
        write_text(installation, re.sub(r'^Owner expiry:.*$', 'Owner expiry: '+expires_at(owner).isoformat(),
                                       installation.read_text(), flags=re.M))
    for context_id, reason in sorted(skipped.items()):
        print(f'Skipped context {context_id}: {reason}. Historical access stays with the old owner '
              'until expiry or revocation; it is not transferred to the replacement.')
    return archive

@locked_installation(1)
def bootstrap(run, root, database):
    """Create the owner, workspace and shared context; resume an interrupted run.

    Each step is persisted before the next one: the owner bootstraps a staged
    database that moves into place only after owner.json is written, and
    local-context.json records the workspace before the context exists.
    """
    owner = root/'credentials/owner.json'
    existing_owner = owner.exists()
    staged = database.with_name(database.name+'.init')
    sidecars = ('', '-wal', '-shm')
    if not database.exists():
        if not owner.exists():
            for suffix in sidecars:
                Path(f'{staged}{suffix}').unlink(missing_ok=True)
            document = run('bootstrap', '--name', 'Local owner', '--ttl', duration(ONE_YEAR+MARGIN),
                           bootstrap=secrets.token_urlsafe(32), database=staged)
            private_json(owner, document)
        if not staged.exists():
            raise RuntimeError(f'{owner} exists without its database {database}; restore the database or remove both.')
        for suffix in sidecars:
            if Path(f'{staged}{suffix}').exists():
                os.replace(f'{staged}{suffix}', f'{database}{suffix}')
        database.chmod(0o600)
    path = root/'local-context.json'
    state = json.loads(path.read_text()) if path.exists() else {}
    if existing_owner or not owner.exists():
        validate_credential(run, owner, state.get('context_id'))
    if 'workspace_id' not in state:
        state['workspace_id'] = run('workspace-create', '--name', 'Local workspace', token=token(owner))['ID']
        write_text(path, json.dumps(state, indent=2)+'\n', mode=0o600)
    if 'context_id' not in state:
        state['context_id'] = run('context-create', '--workspace-id', state['workspace_id'], '--name', 'Local agents',
                                  token=token(owner))['ID']
        write_text(path, json.dumps(state, indent=2)+'\n', mode=0o600)
    return state

# Harnesses are data: built-in entries in scripts/local/harnesses.json, plus
# the ones registered with add-harness in <root>/harnesses.json.

def harnesses(root):
    table = json.loads((LOCAL/'harnesses.json').read_text())
    registry = root/'harnesses.json'
    if registry.exists():
        for name, spec in json.loads(registry.read_text()).items():
            for key in ('skills_dir', 'other_skills_dirs', 'env_vars'):
                if key in table.get(name, {}):
                    spec.setdefault(key, table[name][key])
            table[name] = spec
    return table

def remember(root, name, spec):
    registry = root/'harnesses.json'
    table = json.loads(registry.read_text()) if registry.exists() else {}
    table[name] = spec
    write_text(registry, json.dumps(table, indent=2)+'\n', mode=0o600)

def check(name, spec):
    if not NAME.match(name) or name in ('owner', 'viewer', 'sessions'):
        raise ValueError(f'Invalid harness name {name!r}: use lowercase letters, digits, - and _ (not owner/viewer/sessions)')
    mcp, approval = spec.get('mcp') or {}, spec.get('approval') or {}
    if not spec.get('instructions') or not mcp.get('file'):
        raise ValueError(f'Harness {name!r} needs an instruction file and an MCP config file')
    if mcp.get('format') not in ('toml', 'json'):
        raise ValueError(f'Unknown MCP config format {mcp.get("format")!r}; use toml or json')
    if approval.get('format') not in (None, 'entry', 'list'):
        raise ValueError(f'Unknown approval format {approval["format"]!r}; use entry or list')
    if approval and not all(approval.get(k) for k in ('key', 'value')+(('file',) if approval['format'] == 'list' else ())):
        raise ValueError(f'Incomplete approval for harness {name!r}')

def toml_value(value):
    if isinstance(value, bool):
        return 'true' if value else 'false'
    if isinstance(value, (int, float)):
        return str(value)
    if isinstance(value, str):
        return json.dumps(value)
    if isinstance(value, list):
        return '['+', '.join(toml_value(v) for v in value)+']'
    raise ValueError(f'Unsupported TOML value {value!r}')

def toml_lines(text):
    """Yield (line, outside) where outside means the line does not continue a multi-line value."""
    depth, quote = 0, None
    for line in text.splitlines(keepends=True):
        outside = depth == 0 and quote is None
        i = 0
        while i < len(line):
            if quote:
                if line.startswith(quote, i):
                    quote, i = None, i+len(quote)
                    continue
                if line[i] == '\\' and quote in ('"', '"""'):
                    i += 2
                    continue
                if line[i] == '\n' and len(quote) == 1:
                    quote = None
                i += 1
                continue
            if line[i] == '#':
                break
            for opening in ('"""', "'''", '"', "'"):
                if line.startswith(opening, i):
                    quote, i = opening, i+len(opening)
                    break
            else:
                depth += {'[': 1, '{': 1, ']': -1, '}': -1}.get(line[i], 0)
                i += 1
        yield line, outside

def set_toml_table(text, table, entry):
    """Set entry's keys in [table], keeping its other keys, comments and all other tables."""
    header = re.compile(r'^\s*\[\s*'+r'\s*\.\s*'.join(map(re.escape, table.split('.')))+r'\s*\]\s*(#.*)?$')
    lines = list(toml_lines(text))
    start = next((i for i, (line, outside) in enumerate(lines) if outside and header.match(line)), None)
    body = [f'{k} = {toml_value(v)}\n' for k, v in entry.items()]
    if start is None:
        prefix = text if not text or text.endswith('\n') else text+'\n'
        result = prefix+('\n' if prefix else '')+f'[{table}]\n'+''.join(body)
    else:
        end = next((i for i in range(start+1, len(lines)) if lines[i][1] and lines[i][0].lstrip().startswith('[')),
                   len(lines))
        kept, dropping = [], False
        for line, outside in lines[start+1:end]:
            if outside:
                key = TOML_KEY.match(line)
                decoded = key[1] if key else None
                if key and tomllib:
                    decoded = next(iter(tomllib.loads(key[1]+' = 0')))
                dropping = decoded in entry
            if not dropping:
                kept.append(line)
        while kept and not kept[-1].strip():
            kept.pop()
        tail = ['\n'] if end < len(lines) else []
        result = ''.join([line for line, _ in lines[:start+1]]+body+kept+tail+[line for line, _ in lines[end:]])
    if tomllib:
        try:
            tomllib.loads(text)
        except tomllib.TOMLDecodeError as error:
            raise ValueError(f'Refusing to edit a file that is not valid TOML: {error}')
        try:
            value = tomllib.loads(result)
            for part in table.split('.'):
                value = value[part]
        except (tomllib.TOMLDecodeError, KeyError, TypeError) as error:
            raise ValueError(f'Refusing to write invalid TOML for [{table}]: {error}')
        if any(value.get(k) != v for k, v in entry.items()):
            raise ValueError(f'Refusing to write [{table}]: the edited table does not hold the new entry')
    return result

def json_document(path):
    return json.loads(path.read_text() or '{}') if path.exists() else {}

def dotted(document, key):
    """Return the container at a dotted key's parent and its last component."""
    *parents, last = key.split('.')
    for part in parents:
        document = document.setdefault(part, {})
    return document, last

def merge_env_vars(current, entry):
    if not entry.get('env_vars'):
        return entry
    values = current.get('env_vars', [])
    if not isinstance(values, list) or any(not isinstance(value, str) for value in values):
        raise ValueError('MCP env_vars must be an array of strings')
    return {**entry, 'env_vars': list(dict.fromkeys(values+entry['env_vars']))}

def register_mcp(mcp, entry, home, backups):
    path = expand(mcp['file'], home)
    if mcp['format'] == 'toml':
        text = path.read_text() if path.exists() else ''
        if entry.get('env_vars'):
            if tomllib is None:
                raise RuntimeError('Environment forwarding requires Python 3.11 or newer.')
            current = lookup(tomllib.loads(text), f'{mcp.get("key", "mcp_servers")}.{SERVER}') or {}
            entry = merge_env_vars(current, entry)
        write_text(path, set_toml_table(text, f'{mcp.get("key", "mcp_servers")}.{SERVER}', entry), backups)
    else:
        document = json_document(path)
        servers, last = dotted(document, mcp.get('key', 'mcpServers'))
        current = servers.setdefault(last, {}).get(SERVER, {})
        servers[last][SERVER] = {**current, 'type': 'stdio', **merge_env_vars(current, entry)}
        write_text(path, json.dumps(document, indent=2)+'\n', backups)

def allow_listed(approval, home, backups):
    path = expand(approval['file'], home)
    document = json_document(path)
    parent, last = dotted(document, approval['key'])
    values = parent.setdefault(last, [])
    if approval['value'] not in values:
        values.append(approval['value'])
    write_text(path, json.dumps(document, indent=2)+'\n', backups)

def lookup(document, key):
    for part in key.split('.'):
        if not isinstance(document, dict) or part not in document:
            return None
        document = document[part]
    return document

def harness_entry(name, spec, root):
    entry = {'command': str(root/'bin/comitia-client'), 'args': [name, 'mcp']}
    approval = spec.get('approval') or {}
    if approval.get('format') == 'entry':
        entry[approval['key']] = approval['value']
    if spec.get('env_vars'):
        entry['env_vars'] = spec['env_vars']
    return entry

def remember_approval(root, approval, home):
    """Record only installer-added approvals; preexisting user approvals stay owned by the user."""
    path = root/'managed-harnesses.json'
    managed = json_document(path)
    values = lookup(json_document(expand(approval['file'], home)), approval['key']) or []
    if approval['value'] not in values and approval not in managed.setdefault('approvals', []):
        managed['approvals'].append(approval)
        write_text(path, json.dumps(managed, indent=2)+'\n', mode=0o600)

def remember_harness(root, name, spec):
    path = root/'managed-harnesses.json'
    managed = json_document(path)
    entry = {'name': name, 'spec': spec}
    if entry not in managed.setdefault('harnesses', []):
        managed['harnesses'].append(entry)
        write_text(path, json.dumps(managed, indent=2)+'\n', mode=0o600)

def warn_shadow_skills(spec, home):
    directories = [spec['skills_dir']] if spec.get('skills_dir') else []
    directories += spec.get('other_skills_dirs', [])
    seen = set()
    for directory in directories:
        directory = expand(directory, home)
        if not directory.is_dir():
            continue
        for path in sorted(directory.iterdir()):
            if path.name == 'comitia' or not path.is_dir() or path.absolute() in seen:
                continue
            seen.add(path.absolute())
            mentions = False
            try:
                text = (path/'SKILL.md').read_text()
                lines = text.splitlines()
                if lines and lines[0].strip() == '---':
                    end = next((i for i in range(1, len(lines)) if lines[i].strip() == '---'), None)
                    mentions = end is not None and bool(re.search(r'\bcomitia\b', '\n'.join(lines[1:end]), re.I))
            except (OSError, UnicodeError):
                pass
            if path.name.lower().startswith('comitia') or mentions:
                print(f'Warning: potentially shadowing Comitia skill: {path}')

def skill_files(path):
    """Fingerprint the whole tree; extra files, directories and links are edits."""
    if path.is_symlink() or not path.is_dir():
        return None
    files = {}
    for item in sorted(path.rglob('*')):
        if item.is_symlink():
            return None
        key = str(item.relative_to(path))
        if item.is_dir():
            files[key] = 'directory'
        elif item.is_file():
            files[key] = hashlib.sha256(item.read_bytes()).hexdigest()
        else:
            return None
    return files

def skill_symlink(root, path):
    boundary = Path(os.path.commonpath((root.absolute(), path.absolute())))
    for parent in (path, *path.parents):
        if parent == boundary:
            break
        if parent.is_symlink():
            return True
    return False

def install_skill(root, destination):
    """Replace only our unchanged skill, retaining unowned or modified trees."""
    source = REPO/'skills/comitia'
    files = skill_files(source)
    if not files or 'SKILL.md' not in files:
        raise RuntimeError(f'Comitia skill is missing from {source}')
    manifest = root/'managed-skills.json'
    managed = json_document(manifest)
    key = str(destination.absolute())
    previous = managed.get(key)
    current = skill_files(destination)
    if skill_symlink(root, destination):
        print(f'Preserved symlinked Comitia skill path: {destination}')
        return False
    if destination.exists() and (not previous or current not in (previous['files'], files)):
        print(f'Preserved unowned or modified Comitia skill: {destination}')
        return False
    if current != files:
        destination.parent.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(prefix='.comitia-skill-', dir=destination.parent) as temporary:
            staging = Path(temporary)/'new'
            shutil.copytree(source, staging)
            old = Path(temporary)/'old'
            if destination.exists():
                backups = root/'backups'
                backups.mkdir(parents=True, exist_ok=True, mode=0o700)
                shutil.copytree(destination, backups/('skill-'+secrets.token_hex(8)))
                os.replace(destination, old)
            try:
                os.replace(staging, destination)
            except OSError:
                if old.exists():
                    os.replace(old, destination)
                raise
    managed[key] = {'version': SKILL_VERSION, 'files': files}
    write_text(manifest, json.dumps(managed, indent=2)+'\n', mode=0o600)
    return True

def remove_skills(root):
    manifest = root/'managed-skills.json'
    managed, retained = json_document(manifest), []
    for path, record in list(managed.items()):
        destination = Path(path)
        if skill_symlink(root, destination) or (
                destination.exists() and skill_files(destination) != record['files']):
            retained.append(path)
            continue
        if destination.exists():
            shutil.rmtree(destination)
        del managed[path]
    if manifest.exists():
        write_text(manifest, json.dumps(managed, indent=2)+'\n', mode=0o600)
    return retained

def remove_toml_entry(text, table, entry):
    if tomllib is None:
        raise RuntimeError('Selective TOML uninstall requires Python 3.11 or newer.')
    document = tomllib.loads(text)
    current = lookup(document, table)
    if not isinstance(current, dict) or any(current.get(k) != entry[k] for k in ('command', 'args')):
        return text
    remove = {k for k, v in entry.items() if current.get(k) == v}
    expected = copy.deepcopy(document)
    remaining = lookup(expected, table)
    for key in remove:
        del remaining[key]
    parent, _, name = table.rpartition('.')
    if not remaining:
        (lookup(expected, parent) if parent else expected).pop(name)
    header = re.compile(r'^\s*\[\s*'+r'\s*\.\s*'.join(map(re.escape, table.split('.')))+r'\s*\]\s*(#.*)?$')
    lines = list(toml_lines(text))
    start = next((i for i, (line, outside) in enumerate(lines) if outside and header.match(line)), None)
    if start is None:
        return text  # Inline/quoted tables were not written by the installer.
    end = next((i for i in range(start+1, len(lines)) if lines[i][1] and lines[i][0].lstrip().startswith('[')), len(lines))
    kept, dropping = [], False
    assignment = re.compile(r'''^\s*([A-Za-z0-9_-]+|'[^']*'|"(?:[^"\\]|\\.)*")\s*=''')
    for line, outside in lines[start+1:end]:
        if outside:
            key = assignment.match(line)
            # Let TOML decode quoted/escaped keys instead of comparing source spellings.
            dropping = bool(key and next(iter(tomllib.loads(key[1]+' = 0'))) in remove)
        if not dropping:
            kept.append(line)
    prefix = [line for line, _ in lines[:start]]
    if current.keys()-remove:
        prefix.append(lines[start][0])
    result = ''.join(prefix+kept+[line for line, _ in lines[end:]])
    parsed = tomllib.loads(result)
    if not remaining and parent and lookup(parsed, parent) is None:
        # Removing the only child must not erase its unrelated parent table.
        result = ''.join(prefix+[f'[{parent}]\n']+kept+[line for line, _ in lines[end:]])
        parsed = tomllib.loads(result)
    if parsed != expected:
        raise ValueError(f'Refusing to remove [{table}]: result would change unrelated TOML or retain managed fields')
    return result

@locked_installation(0)
def remove_harnesses(root, home):
    """Remove matching managed fields/blocks, retaining changed entries and user settings."""
    registry = root/'harnesses.json'
    retained = []
    manifest = root/'managed-harnesses.json'
    managed = json_document(manifest)
    entries = managed.get('harnesses', [])
    for name, spec in json_document(registry).items():
        entry = {'name': name, 'spec': spec}
        if entry not in entries:
            entries.append(entry)
    for recorded in entries:
        name, spec = recorded['name'], recorded['spec']
        entry, mcp = harness_entry(name, spec, root), spec['mcp']
        path = expand(mcp['file'], home)
        if path.exists():
            if mcp['format'] == 'toml':
                text = path.read_text()
                updated = remove_toml_entry(text, f'{mcp.get("key", "mcp_servers")}.{SERVER}', entry)
                write_text(path, updated, root/'backups')
                if updated == text and lookup(tomllib.loads(text), f'{mcp.get("key", "mcp_servers")}.{SERVER}'):
                    retained.append(str(path))
            else:
                document = json_document(path)
                servers = lookup(document, mcp.get('key', 'mcpServers'))
                current = servers.get(SERVER) if isinstance(servers, dict) else None
                if isinstance(current, dict) and all(current.get(k) == entry[k] for k in ('command', 'args')):
                    for k, v in {'type': 'stdio', **entry}.items():
                        if current.get(k) == v:
                            current.pop(k)
                    if not current:
                        servers.pop(SERVER)
                    write_text(path, json.dumps(document, indent=2)+'\n', root/'backups')
                elif current:
                    retained.append(str(path))
        instructions = expand(spec['instructions'], home)
        if instructions.exists():
            text = instructions.read_text()
            updated = BLOCK.sub(lambda m: '' if str(root/'local-context.json') in m[0] else m[0], text)
            write_text(instructions, updated, root/'backups')
    for approval in managed.get('approvals', []):
        config = expand(approval['file'], home)
        if config.exists():
            document = json_document(config)
            values = lookup(document, approval['key'])
            if isinstance(values, list) and approval['value'] in values:
                values.remove(approval['value'])
                write_text(config, json.dumps(document, indent=2)+'\n', root/'backups')
    if manifest.exists():
        write_text(manifest, json.dumps({'harnesses': [], 'approvals': []}, indent=2)+'\n', mode=0o600)
    return retained+remove_skills(root)

def render_instructions(name, spec, local, root):
    template = string.Template((LOCAL/'instructions.md').read_text())
    return template.substitute(identity=name, display=spec.get('display', name), context_id=local['context_id'],
                               server_url=local['server_url'], local_context=root/'local-context.json',
                               skill_path=root/'skills/comitia/SKILL.md')

def place_block(text, block):
    """Replace any managed block (any version) with block, or put it first."""
    if BLOCK.search(text):
        first = True
        def swap(_):
            nonlocal first
            keep, first = first, False
            return block if keep else ''
        return BLOCK.sub(swap, text)
    return block+('\n'+text if text else '')

@locked_installation(2)
def add_harness(name, spec, root, home, run, permissions='read,write,claim,grant'):
    """Mint the harness credential once, register the MCP server and its auto-approval, render instructions."""
    check(name, spec)
    warn_shadow_skills(spec, home)
    install_skill(root, root/'skills/comitia')
    if spec.get('skills_dir'):
        install_skill(root, expand(spec['skills_dir'], home)/'comitia')
    backups = root/'backups'
    local = json.loads((root/'local-context.json').read_text())
    credential = root/'credentials'/f'{name}.json'
    if credential.exists():
        validate_credential(run, credential, local['context_id'])
    if not credential.exists():
        owner = root/'credentials/owner.json'
        ttl = child_ttl(owner)
        session = run('delegate', '--name', name, '--ttl', ttl, '--context-id', local['context_id'],
                      '--permissions', permissions, '--grant-ttl', ttl, token=token(owner))
        private_json(credential, session)
    entry = harness_entry(name, spec, root)
    approval = spec.get('approval') or {}
    # Track ownership before editing files, including interrupted registrations
    # and previous config locations when a harness is reconfigured.
    remember_harness(root, name, spec)
    register_mcp(spec['mcp'], entry, home, backups)
    if approval.get('format') == 'list':
        remember_approval(root, approval, home)
        allow_listed(approval, home, backups)
    path = expand(spec['instructions'], home)
    text = path.read_text() if path.exists() else ''
    write_text(path, place_block(text, render_instructions(name, spec, local, root)), backups)
    remember(root, name, spec)
