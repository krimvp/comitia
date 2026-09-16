#!/usr/bin/env python3
"""Canonical local client: persistent HTTP by default, isolated scopes per run."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path.home() / 'Library/Application Support/Comitia'
BINARY = ROOT / 'bin/comitia'
CONFIG = ROOT / 'service.json'

def credential(path):
    document = json.loads(Path(path).expanduser().read_text())
    return document.get('credential', document)['Token']

def main():
    config = json.loads(CONFIG.read_text())
    default_server = config['server_url']
    args = sys.argv[1:]
    if args and args[0] == 'new-session':
        parser = argparse.ArgumentParser(description='Create a scoped session on the persistent local service.')
        parser.add_argument('--name', required=True, help='Agent/session display name')
        parser.add_argument('--context-name', help='New conversation name; defaults to the session name')
        parser.add_argument('--context-id', help='Join an existing owner-readable context instead')
        parser.add_argument('--permissions', default='read,write,claim,grant')
        parser.add_argument('--ttl', default='24h')
        parser.add_argument('--grant-ttl', default='23h')
        opts = parser.parse_args(args[1:])
        # This administrative operation deliberately uses the installed local owner,
        # never a caller's remote endpoint or another session's credentials.
        env = {k:v for k,v in os.environ.items() if not k.startswith('COMITIA_')}
        env.update(COMITIA_SERVER_URL=default_server, COMITIA_CREDENTIAL=credential(ROOT/'credentials/owner.json'))
        def run(*command):
            result = subprocess.run([str(BINARY), *command], env=env, capture_output=True, text=True)
            if result.returncode:
                raise RuntimeError(result.stderr.strip())
            return json.loads(result.stdout)
        if opts.context_id:
            context_id = opts.context_id
            readable = False
            after = ''
            while True:
                page = run('context-list', '--after-id', after, '--limit', '100')
                if any(c['ID'] == context_id for c in page): readable = True; break
                if len(page) < 100: break
                after = page[-1]['ID']
            if not readable:
                raise RuntimeError('The local owner cannot read that context; no session was created.')
        else:
            initial = json.loads((ROOT/'local-context.json').read_text())
            context = run('context-create', '--workspace-id', initial['workspace_id'], '--name', opts.context_name or opts.name)
            context_id = context['ID']
        session = run('delegate', '--name', opts.name, '--context-id', context_id,
                      '--permissions', opts.permissions, '--ttl', opts.ttl, '--grant-ttl', opts.grant_ttl)
        session['server_url'] = default_server
        session['context_id'] = context_id
        directory = ROOT/'sessions'
        directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        directory.chmod(0o700)
        path = directory/(session['credential']['Session']['ID']+'.json')
        fd = os.open(path, os.O_WRONLY|os.O_CREAT|os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as stream:
            json.dump(session, stream, indent=2)
            stream.write('\n')
        print(json.dumps({'server_url':default_server,'context_id':context_id,
                          'session_id':session['credential']['Session']['ID'],
                          'session_file':str(path),'expires_at':session['credential']['Session']['ExpiresAt']}))
        return
    env = dict(os.environ)
    session_file = env.get('COMITIA_SESSION_FILE')
    # Wrapper-only flags precede the ordinary CLI flags/command.
    if args and args[0] == '--session-file':
        if len(args)<3: raise RuntimeError('Usage: comitia --session-file PATH COMMAND [FLAGS]')
        session_file,args = args[1],args[2:]
    # Only flags before the subcommand are global; message bodies are data.
    global_flags = set()
    index = 0
    while index < len(args) and args[index].startswith('-'):
        option = '--' + args[index].split('=', 1)[0].lstrip('-')
        global_flags.add(option)
        index += 1 if '=' in args[index] or option in ('--help', '--h') else 2
    explicit_db = '--db' in global_flags
    explicit_server = '--server-url' in global_flags
    if index < len(args) and args[index] in ('ws', 'join', 'post', 'view', 'decide', 'resolve', 'release', 'wait'):
        raise RuntimeError('That command belongs to the legacy embedded board. For new work use comitia new-session, then the HTTP session API. Existing board runs must keep their original binary/database.')
    if explicit_db and not explicit_server:
        env.pop('COMITIA_SERVER_URL', None)
    else:
        env['COMITIA_SERVER_URL'] = env.get('COMITIA_SERVER_URL') or default_server
    if session_file:
        session = json.loads(Path(session_file).expanduser().read_text())
        selected_server = session.get('server_url', default_server)
        if explicit_server or explicit_db or '--credential' in global_flags or env.get('COMITIA_SERVER_URL') != selected_server:
            raise RuntimeError('Session files are pinned to their server. Do not combine one with an endpoint/database override.')
        env['COMITIA_CREDENTIAL'] = session.get('credential', session)['Token']
    os.execve(str(BINARY), [str(BINARY), *args], env)

if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError, KeyError, RuntimeError) as error:
        print(json.dumps({'error':str(error)}), file=sys.stderr)
        sys.exit(1)
