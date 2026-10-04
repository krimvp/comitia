"""Shared steps of the local installers: binary, owner bootstrap and harness registration.

Every function takes the installation root and home directory explicitly, so
the installers and their tests never depend on the caller's real home.
"""
import datetime
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import string
import subprocess

REPO = Path(__file__).resolve().parent.parent
LOCAL = REPO/'scripts/local'
NAME = re.compile(r'^[a-z0-9][a-z0-9_-]{0,63}$')
BLOCK = re.compile(r'<!-- comitia-local:start(?: v\d+)? -->.*?<!-- comitia-local:end -->\n?', re.S)
TOML_KEY = re.compile(r'^\s*([A-Za-z0-9_-]+)\s*=')
ONE_YEAR = '8760h'
SERVER = 'comitia'

def expand(path, home):
    """Expand a leading ~ against home rather than the process's HOME."""
    path = str(path)
    if path == '~' or path.startswith('~/'):
        return Path(home)/path[2:]
    return Path(path)

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

def install_binary(root, source=None):
    """Install bin/comitia from source, or build it from this checkout."""
    binary = root/'bin/comitia'
    staged = root/'bin/comitia.new'
    if source:
        shutil.copy2(Path(source).resolve(), staged)
    else:
        subprocess.run(['go', 'build', '-o', str(staged), './cmd/comitia'], cwd=REPO, check=True)
    staged.chmod(0o700)
    os.replace(staged, binary)
    return binary

def runner(binary, database):
    """Return run(*command, token=None) against the embedded database."""
    # Exclude inherited credentials and server routing from local administration.
    base = {k:v for k,v in os.environ.items() if not k.startswith('COMITIA_')}
    base['COMITIA_DB'] = str(database)
    def run(*command, token=None, bootstrap=None):
        env = dict(base)
        if token:
            env['COMITIA_CREDENTIAL'] = token
        if bootstrap:
            env['COMITIA_BOOTSTRAP_CREDENTIAL'] = bootstrap
        result = subprocess.run([str(binary), *command], env=env, capture_output=True, text=True)
        if result.returncode:
            raise RuntimeError(f'Comitia {command[0]} failed: {result.stderr.strip()}')
        return json.loads(result.stdout)
    return run

def bootstrap(run, root, database):
    """Create the owner, workspace and shared context once; return their IDs or None."""
    if database.exists():
        return None
    owner = run('bootstrap', '--name', 'Local owner', '--ttl', ONE_YEAR, bootstrap=secrets.token_urlsafe(32))
    private_json(root/'credentials/owner.json', owner)
    owner_token = owner['credential']['Token']
    workspace = run('workspace-create', '--name', 'Local workspace', token=owner_token)
    context = run('context-create', '--workspace-id', workspace['ID'], '--name', 'Local agents', token=owner_token)
    database.chmod(0o600)
    return {'workspace_id': workspace['ID'], 'context_id': context['ID']}

# Harnesses are data: built-in entries in scripts/local/harnesses.json, plus
# the ones registered with add-harness in <root>/harnesses.json.

def harnesses(root):
    table = json.loads((LOCAL/'harnesses.json').read_text())
    registry = root/'harnesses.json'
    if registry.exists():
        table.update(json.loads(registry.read_text()))
    return table

def remember(root, name, spec):
    registry = root/'harnesses.json'
    table = json.loads(registry.read_text()) if registry.exists() else {}
    table[name] = spec
    write_text(registry, json.dumps(table, indent=2)+'\n', mode=0o600)

def check(name, spec):
    if not NAME.match(name) or name in ('owner', 'viewer'):
        raise ValueError(f'Invalid harness name {name!r}: use lowercase letters, digits, - and _ (not owner/viewer)')
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

def set_toml_table(text, table, entry):
    """Set entry's keys in [table], keeping its other single-line keys and all other tables."""
    header = f'[{table}]'
    lines = text.splitlines(keepends=True)
    start = next((i for i, line in enumerate(lines) if line.strip() == header), None)
    body = [f'{k} = {toml_value(v)}\n' for k, v in entry.items()]
    if start is None:
        prefix = text if not text or text.endswith('\n') else text+'\n'
        return prefix+('\n' if prefix else '')+header+'\n'+''.join(body)
    end = next((i for i in range(start+1, len(lines)) if lines[i].lstrip().startswith('[')), len(lines))
    kept = [line for line in lines[start+1:end] if not (TOML_KEY.match(line) and TOML_KEY.match(line).group(1) in entry)]
    while kept and not kept[-1].strip():
        kept.pop()
    tail = ['\n'] if end < len(lines) else []
    return ''.join(lines[:start+1]+body+kept+tail+lines[end:])

def json_document(path):
    return json.loads(path.read_text() or '{}') if path.exists() else {}

def dotted(document, key):
    """Return the container at a dotted key's parent and its last component."""
    *parents, last = key.split('.')
    for part in parents:
        document = document.setdefault(part, {})
    return document, last

def register_mcp(mcp, entry, home, backups):
    path = expand(mcp['file'], home)
    if mcp['format'] == 'toml':
        text = path.read_text() if path.exists() else ''
        write_text(path, set_toml_table(text, f'{mcp.get("key", "mcp_servers")}.{SERVER}', entry), backups)
    else:
        document = json_document(path)
        servers, last = dotted(document, mcp.get('key', 'mcpServers'))
        current = servers.setdefault(last, {}).get(SERVER, {})
        servers[last][SERVER] = {**current, 'type': 'stdio', **entry}
        write_text(path, json.dumps(document, indent=2)+'\n', backups)

def allow_listed(approval, home, backups):
    path = expand(approval['file'], home)
    document = json_document(path)
    parent, last = dotted(document, approval['key'])
    values = parent.setdefault(last, [])
    if approval['value'] not in values:
        values.append(approval['value'])
    write_text(path, json.dumps(document, indent=2)+'\n', backups)

def render_instructions(name, spec, local, root):
    template = string.Template((LOCAL/'instructions.md').read_text())
    return template.substitute(identity=name, display=spec.get('display', name), context_id=local['context_id'],
                               server_url=local['server_url'], local_context=root/'local-context.json')

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

def add_harness(name, spec, root, home, run, permissions='read,write,claim,grant'):
    """Mint the harness credential once, register the MCP server and its auto-approval, render instructions."""
    check(name, spec)
    backups = root/'backups'
    local = json.loads((root/'local-context.json').read_text())
    credential = root/'credentials'/f'{name}.json'
    if not credential.exists():
        session = run('delegate', '--name', name, '--ttl', ONE_YEAR, '--context-id', local['context_id'],
                      '--permissions', permissions, '--grant-ttl', ONE_YEAR, token=token(root/'credentials/owner.json'))
        private_json(credential, session)
    entry = {'command': str(root/'bin/comitia-client'), 'args': [name, 'mcp']}
    approval = spec.get('approval') or {}
    if approval.get('format') == 'entry':
        entry[approval['key']] = approval['value']
    register_mcp(spec['mcp'], entry, home, backups)
    if approval.get('format') == 'list':
        allow_listed(approval, home, backups)
    path = expand(spec['instructions'], home)
    text = path.read_text() if path.exists() else ''
    write_text(path, place_block(text, render_instructions(name, spec, local, root)), backups)
    remember(root, name, spec)
