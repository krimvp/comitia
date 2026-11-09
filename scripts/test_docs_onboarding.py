"""Documentation contracts and executable recipes. Run: python3 -m unittest scripts/test_docs_onboarding.py."""
import json
import os
from pathlib import Path
import re
import shlex
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


def read(name):
    return (ROOT / name).read_text()


def block(text, marker, language='sh'):
    assert marker in text, f'Missing documented section: {marker}'
    section = text.split(marker, 1)[1]
    match = re.search(r'```' + language + r'\n(.*?)\n```', section, re.S)
    assert match, f'Missing {language} example in {marker}'
    return match[1]


class OnboardingContracts(unittest.TestCase):
    def test_portable_connections_and_demo_handoff(self):
        guide = read('docs/CONNECTING.md')
        for name in ('comitia-client', 'comitia-local', 'demo-mcp', 'COMITIA_MCP_URL'):
            self.assertIn(name, guide)
        self.assertNotRegex(guide, r'/opt/comitia|already configured')
        self.assertIn('agent_json', read('docs/USING-COMITIA.md'))
        self.assertIn('credential-file', guide)
        forwarding = block(guide, 'forward **both** ports')
        self.assertIn('-L 18390:127.0.0.1:8390', forwarding)
        self.assertIn('-L 18391:127.0.0.1:8391', forwarding)
        self.assertIn('http://127.0.0.1:18391/mcp', guide)

    def test_schema_rules_have_one_owner(self):
        guide = read('docs/USING-COMITIA.md')
        self.assertIn('message-protocol.md#envelope-contract', guide)
        self.assertNotIn('| schema | required payload |', guide)
        envelope = json.loads(block(guide, '## Addressed messages', 'json'))
        self.assertEqual(envelope['schema'], 'comitia.assignment.v1')
        self.assertTrue(envelope['task_id'])
        self.assertEqual(set(envelope['payload']), {'instructions', 'acceptance_criteria'})

    def test_complete_renewal_pair(self):
        guide = read('docs/task-validation.md')
        recipe = block(guide, '## Complete lease loop')
        self.assertIn('task-create', recipe)
        self.assertIn('task-claim', recipe)
        self.assertIn('task-renew', recipe)
        self.assertIn('task-complete', recipe)
        self.assertEqual(recipe.count("jq -r '.Token'"), 2)
        self.assertEqual(recipe.count("jq -r '.Task.LeaseVersion'"), 2)
        self.assertIn('86400', guide)

    def test_wait_and_prune_capabilities(self):
        for name in ('docs/architecture.md', 'docs/local-daemon.md'):
            text = read(name)
            self.assertNotRegex(text, r'not (?:provide|expose) SSE or long polling')
            self.assertIn('long polling', text)
        text = read('docs/security.md') + read('docs/USING-COMITIA.md')
        self.assertNotIn('planned in #9', text)
        self.assertIn('comitia-client sessions --prune', text)
        self.assertIn('wait_messages', read('docs/local-sessions.md'))

    def test_macos_shared_context_and_configs(self):
        guide = read('docs/local-sessions.md')
        recipe = block(guide, '## Connect two harnesses')
        self.assertIn('--context-id "$context_id"', recipe)
        self.assertIn('session_a', recipe)
        self.assertIn('session_b', recipe)
        self.assertIn('[mcp_servers.comitia]', guide)
        self.assertIn('"mcpServers"', guide)
        self.assertIn('task-claim', guide)
        self.assertIn('"$HOME/Library/Application Support/Comitia/bin/comitia-local"', guide)

    def test_landing_starts_with_the_agent_setup_prompt(self):
        html = read('site/index.html')
        start = html.split('<section id="start"', 1)[1]
        self.assertIn('Set up Comitia from https://github.com/krimvp/comitia: follow skills/comitia/SKILL.md.', start)
        self.assertIn('docs/local-service.md', start)
        self.assertNotIn('install_linux_service.py', start, 'manual install steps belong in the docs')

    @unittest.skipUnless(shutil.which('go') and shutil.which('jq'), 'go and jq are required for executable recipes')
    def test_executable_demo_and_lease_recipe(self):
        using = read('docs/USING-COMITIA.md')
        setup = block(using, '## Isolated demo')
        lease = block(read('docs/task-validation.md'), '## Complete lease loop')
        handoff = block(read('docs/CONNECTING.md'), '## Temporary demo')
        macos = block(read('docs/local-sessions.md'), '## Connect two harnesses')
        assignment = json.loads(block(using, '## Addressed messages', 'json'))
        assignment_template = shlex.quote(json.dumps(assignment))
        mcp_steps = []
        table = read('docs/task-validation.md')
        for name in ('create_task', 'claim_task', 'renew_task', 'complete_task'):
            arguments = json.loads(re.search(r'\| `' + name + r'` \| `(.*?)`', table)[1])
            request = {'jsonrpc': '2.0', 'id': 1, 'method': 'tools/call',
                       'params': {'name': name, 'arguments': arguments}}
            raw = shlex.quote(json.dumps(request))
            mcp_steps.append(f"printf 'MCP {name}\\n' >&2")
            mcp_steps.append(f"mcp_json=$(printf '%s' {raw} | jq -c --arg context \"$context_id\" --arg task \"${{mcp_task_id:-}}\" --arg token \"${{mcp_token:-}}\" "
                             "'walk(if type == \"string\" then if . == \"CONTEXT_ID\" then $context elif . == \"TASK_ID\" then $task elif . == \"CLAIM_TOKEN\" or . == \"NEW_TOKEN\" then $token else . end else . end)' "
                             "| demo_call)")
            mcp_steps.append("printf '%s' \"$mcp_json\" | jq -e '.result.isError != true and .result.structuredContent.result != null' >/dev/null")
            if name == 'create_task':
                mcp_steps.append('mcp_task_id=$(printf "%s" "$mcp_json" | jq -r .result.structuredContent.result.ID)')
            elif name in ('claim_task', 'renew_task'):
                mcp_steps.append('mcp_token=$(printf "%s" "$mcp_json" | jq -r .result.structuredContent.result.Token)')
            else:
                mcp_steps.append('test "$(printf "%s" "$mcp_json" | jq -r .result.structuredContent.result.Status)" = completed')

        go_env = dict(os.environ, GOTOOLCHAIN='go1.24.13', GOFLAGS='-buildvcs=false')
        caches = json.loads(subprocess.check_output(
            ['go', 'env', '-json', 'GOMODCACHE', 'GOCACHE', 'GOROOT'], env=go_env, text=True))
        with tempfile.TemporaryDirectory(prefix='comitia-docs-') as home:
            env = {k: v for k, v in os.environ.items() if not k.startswith('COMITIA_')}
            env.update(HOME=home, GOTOOLCHAIN='go1.24.13', GOFLAGS='-buildvcs=false', **caches)
            env.update(PATH=str(Path(caches['GOROOT']) / 'bin') + os.pathsep + env.get('PATH', ''),
                       GOPROXY='off', GOSUMDB='off')
            # The documented launcher uses its saved credential, independent of the caller's environment.
            script = '\n'.join([
                'set -eu', setup,
                'trap \'kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; rm -rf "$demo"\' EXIT',
                handoff,
                '''demo_call() { { printf '%s\\n' '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"docs-test","version":"1"}}}'; cat; } | "$demo/demo-mcp" mcp | jq -c 'if .error then error(.error.message) elif .id == 1 then . else empty end'; }''',
                'COMITIA_CREDENTIAL=invalid "$demo/demo-mcp" server-info >/dev/null',
                "printf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"server_info\",\"arguments\":{}}}' | demo_call | jq -e '.result.structuredContent.result.features | length > 0' >/dev/null",
                *mcp_steps,
                lease,
                'old_token=$(printf "%s" "$claim_json" | jq -r .Token)',
                'old_version=$(printf "%s" "$claim_json" | jq -r .Task.LeaseVersion)',
                'test "$old_token" != "$claim_token"',
                'if "$demo/comitia" task-complete --task-id "$task_id" --claim-token "$old_token" --lease-version "$old_version" 2>"$demo/stale.log"; then exit 1; fi',
                'grep -q "stale task claim" "$demo/stale.log"',
                'test "$(printf "%s" "$completed_json" | jq -r .Status)" = completed',
                'test "$(printf "%s" "$claim_json" | jq -r .Task.LeaseVersion)" -lt "$lease_version"',
                # Install only the macOS wrapper's fixture files under the temporary HOME.
                'mac_root="$HOME/Library/Application Support/Comitia"',
                'mkdir -p "$mac_root/bin" "$mac_root/credentials"',
                'cp "$demo/comitia" "$mac_root/bin/comitia"',
                'cp scripts/comitia_local.py "$mac_root/bin/comitia-local"',
                'chmod 700 "$mac_root/bin/comitia-local"',
                'printf "%s" "$owner_json" > "$mac_root/credentials/owner.json"',
                '''jq -n --arg url "$COMITIA_SERVER_URL" '{server_url:$url}' > "$mac_root/service.json"''',
                '''jq -n --arg id "$workspace_id" '{workspace_id:$id}' > "$mac_root/local-context.json"''',
                macos,
                'test "$(jq -r .context_id "$file_a")" = "$(jq -r .context_id "$file_b")"',
                f'''printf "%s" {assignment_template} | jq --arg task "$task_id" --arg recipient "$(printf "%s" "$session_b" | jq -r .session_id)" ' .task_id=$task | .recipient_session_ids=[$recipient]' > "$demo/assignment.json"''',
                '"$local_wrapper" --session-file "$file_a" send --context-id "$context_id" --dedupe-key assignment --envelope-file "$demo/assignment.json" >/dev/null',

            ])
            result = subprocess.run(['bash', '-c', script], cwd=ROOT, env=env, capture_output=True, text=True, timeout=180)
            self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == '__main__':
    unittest.main()
