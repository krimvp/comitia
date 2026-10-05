"""C56/C71 editorial contracts. Run: python3 -m unittest scripts/test_docs_structure.py."""
from html import unescape
from html.parser import HTMLParser
from pathlib import Path
import re
import tempfile
import unittest
from urllib.parse import unquote, urlsplit

from scripts import comitia_install

ROOT = Path(__file__).resolve().parents[1]
# Whitespace words at issue-67 + merged #63. Review budgets when intentionally
# adding a topic; do not refresh them automatically from the files under test.
WORD_BASELINE = {
    'README.md': 278,
    'docs/CONNECTING.md': 881,
    # Reviewed #65 owner budget for consumer/request/supervisor/merge workflows.
    'docs/durable-coordination.md': 2200,
    'docs/RUNBOOK.md': 17,
    'docs/USING-COMITIA.md': 1338,
    'docs/agent-world.md': 77,
    'docs/architecture.md': 1565,
    'docs/backup-restore.md': 1341,
    'docs/chatgpt-cloud.md': 17,
    'docs/chatgpt-integration.md': 17,
    'docs/child-contexts.md': 596,
    'docs/evaluation.md': 1496,
    'docs/examples.md': 323,
    'docs/execution-telemetry.md': 445,
    'docs/inspector.md': 1505,
    'docs/local-daemon.md': 821,
    'docs/local-service.md': 3980,
    'docs/local-sessions.md': 711,
    'docs/message-protocol.md': 7167,
    'docs/presence.md': 899,
    'docs/release-readiness-20260909.md': 17,
    'docs/security.md': 1989,
    'docs/task-validation.md': 1818,
    'scripts/local/instructions.md': 160,
    'skills/comitia/SKILL.md': 260,
    'skills/comitia/references/setup.md': 640,
    'skills/comitia/references/coordinate.md': 1000,
    'skills/comitia/references/orchestrate.md': 1350,
}
HISTORY = ('RUNBOOK.md', 'chatgpt-cloud.md', 'chatgpt-integration.md',
           'release-readiness-20260909.md')
TOPICS = {
    'schemas': ('docs/message-protocol.md', 'Envelope contract'),
    'instance defaults': ('docs/message-protocol.md', 'Process defaults and overrides'),
    'authorization': ('docs/security.md', 'Authorization invariants'),
    'delegated launchers': ('docs/local-service.md', 'Spawn flow: a delegated session per task owner'),
    'task outcomes': ('docs/task-validation.md', 'Authoritative events and terminal outcomes'),
    'durable consumers': ('docs/durable-coordination.md', 'Process, then acknowledge'),
    'request receipts': ('docs/durable-coordination.md', 'Scoped conversations and durable requests'),
    'merge reconciliation': ('docs/durable-coordination.md', 'Exact-SHA certificates and merge reconciliation'),
    'supervised processes': ('docs/durable-coordination.md', 'Supervised process registry and steering'),
}


def prose(text):
    """Ignore fenced examples, including shell comments that resemble headings."""
    lines, fence = [], None
    for line in text.splitlines():
        match = re.match(r'^\s{0,3}(`{3,}|~{3,})', line)
        if match:
            marks = match[1]
            if fence is None:
                fence = marks
            elif marks[0] == fence[0] and len(marks) >= len(fence):
                fence = None
            continue
        if fence is None:
            lines.append(line)
    return '\n'.join(lines) + '\n'


class HTMLLinks(HTMLParser):
    def __init__(self, text):
        super().__init__()
        self.links, self.anchors = [], set()
        self.feed(text)

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        for key in ('href', 'src'):
            if key in attrs:
                self.links.append(attrs[key])
        if 'id' in attrs:
            self.anchors.add(attrs['id'])
        if tag == 'a' and 'name' in attrs:
            self.anchors.add(attrs['name'])


def heading_anchors(text):
    text = prose(text)
    result, seen = HTMLLinks(text).anchors, {}
    # GitHub keeps underscores/hyphens, removes punctuation and inline markup,
    # and numbers duplicate headings. Support ATX and setext headings.
    titles = [(m.start(), m[1]) for m in re.finditer(r'^\s{0,3}#{1,6}\s+(.+?)(?:\s+#+)?\s*$', text, re.M)]
    titles += [(m.start(), m[1]) for m in re.finditer(r'^(?![|#])([^\n]+)\n\s{0,3}(?:=+|-+)\s*$', text, re.M)]
    for _, title in sorted(titles):
        title = re.sub(r'\[([^\]]+)\]\([^)]+\)', r'\1', title)
        title = unescape(re.sub(r'<[^>]+>', '', title))
        slug = ''.join(c for c in title.lower() if c.isalnum() or c in '_- ')
        slug = slug.replace(' ', '-')
        count = seen.get(slug, 0)
        seen[slug] = count + 1
        result.add(slug + (f'-{count}' if count else ''))
    return result


def links(path):
    text = path.read_text()
    if path.suffix == '.css':
        return re.findall(r'url\(\s*[\"\']?([^\"\')]+)[\"\']?\s*\)', text)
    if path.suffix == '.md':
        text = prose(text)
        # Inline destinations (also images) and reference definitions. Optional
        # quoted titles are excluded. Reference uses resolve through definitions.
        destinations = re.findall(r'!?\[[^\]]*\]\(\s*(<[^>]*>|[^\s)]+)(?:\s+["\'][^\n]*?["\'])?\s*\)', text)
        definitions = dict(re.findall(r'^\s{0,3}\[([^\]]+)\]:\s*(<[^>]*>|\S+)', text, re.M))
        destinations.extend(definitions.values())
        for label, ref in re.findall(r'!?\[([^\]]+)\]\[([^\]]*)\]', text):
            if (ref or label) not in definitions:
                raise AssertionError(f'{path}: undefined link reference {ref or label}')
        destinations.extend(HTMLLinks(text).links)
        return [unescape(x.strip('<>')) for x in destinations]
    return HTMLLinks(text).links


def link_errors(root, path):
    errors = []
    for destination in links(path):
        url = urlsplit(destination)
        # The landing page and managed template use GitHub URLs for local docs.
        prefix = '/krimvp/comitia/blob/main/'
        if url.netloc == 'github.com' and url.path.startswith(prefix):
            target = root / unquote(url.path[len(prefix):])
        elif url.scheme or url.netloc:
            continue
        else:
            target = path.parent / unquote(url.path) if url.path else path
        if not target.exists():
            errors.append(f'{path.relative_to(root)}: missing {destination}')
        elif url.fragment:
            if target.suffix == '.md':
                anchors = heading_anchors(target.read_text())
            else:
                anchors = HTMLLinks(target.read_text()).anchors
            if unquote(url.fragment) not in anchors:
                errors.append(f'{path.relative_to(root)}: missing anchor {destination}')
    return errors


def active_files():
    return [ROOT/'README.md', *sorted((ROOT/'docs').glob('*.md')),
            ROOT/'scripts/local/instructions.md', *sorted((ROOT/'skills/comitia').rglob('*.md'))]


class DocsStructure(unittest.TestCase):
    def test_local_links_and_anchors(self):
        paths = [ROOT/'README.md', *sorted((ROOT/'docs').rglob('*.md')),
                 ROOT/'scripts/local/instructions.md', *sorted((ROOT/'skills/comitia').rglob('*.md'))]
        paths += [p for p in sorted((ROOT/'site').rglob('*'))
                  if p.suffix in ('.md', '.html', '.css', '.svg') and 'node_modules' not in p.parts]
        errors = [error for p in paths for error in link_errors(ROOT, p)]
        self.assertEqual(errors, [], '\n'.join(errors))

    def test_workflow_precedes_reference(self):
        using = prose((ROOT/'docs/USING-COMITIA.md').read_text())
        self.assertEqual(re.findall(r'^## (.+)$', using, re.M)[0], 'First handoff')
        for link in ('CONNECTING.md', 'message-protocol.md#envelope-contract',
                     'local-service.md#spawn-flow-a-delegated-session-per-task-owner'):
            self.assertIn(f']({link})', using)
        inspector = prose((ROOT/'docs/inspector.md').read_text())
        self.assertEqual(re.findall(r'^## (.+)$', inspector, re.M)[0], 'Using the inspector')

    def test_topics_have_one_reference_owner(self):
        for topic, (owner, heading) in TOPICS.items():
            with self.subTest(topic=topic):
                found = [str(p.relative_to(ROOT)) for p in active_files()
                         if heading in re.findall(r'^#{2,3} (.+)$', prose(p.read_text()), re.M)]
                self.assertEqual(found, [owner])
        table_owners = {'schema': 'docs/message-protocol.md',
                        'instance environment': 'docs/message-protocol.md',
                        'transport environment': 'docs/local-daemon.md',
                        'permission': 'docs/security.md',
                        'message filters': 'docs/message-protocol.md',
                        'process registry': 'docs/durable-coordination.md'}
        found = {kind: [] for kind in table_owners}
        for path in active_files():
            text = prose(path.read_text())
            for table in re.findall(r'(?:^\|[^\n]*\n)+', text, re.M):
                header = table.splitlines()[0].lower()
                kinds = []
                if re.search(r'\|\s*schema\s*\|', header) or len(set(re.findall(r'comitia\.[\w-]+\.v\d+', table))) > 1:
                    kinds.append('schema')
                launch_examples = (str(path.relative_to(ROOT)) in (
                    'docs/local-service.md', 'skills/comitia/references/orchestrate.md')
                    and '| per-launch instance |' in table.lower()
                    and ('| harness |' in header or '| setting |' in header))
                if 'COMITIA_AGENT_INSTANCE' in table and not launch_examples:
                    kinds.append('instance environment')
                elif 'COMITIA_' in table and not launch_examples:
                    kinds.append('transport environment')
                if re.search(r'\|\s*permissions?\s*\|', header):
                    kinds.append('permission')
                if len(set(re.findall(r'`(?:reply_to|thread_id|from_session)`', table))) > 1:
                    kinds.append('message filters')
                if 'registry field' in header:
                    kinds.append('process registry')
                for kind in kinds:
                    owner = str(path.relative_to(ROOT))
                    found[kind].append(owner)
                    self.assertEqual(owner, table_owners[kind], f'{kind} table repeated outside its owner: {owner}')
        self.assertEqual(found['schema'], ['docs/message-protocol.md'])
        self.assertEqual(found['message filters'], ['docs/message-protocol.md'])
        self.assertEqual(found['process registry'], ['docs/durable-coordination.md'])
        for owners in found.values():
            self.assertLessEqual(len(owners), 1, 'duplicate reference inventories')

    def test_history_is_dated_and_linked(self):
        for name in HISTORY:
            with self.subTest(record=name):
                archive = ROOT/'docs/history'/name
                self.assertTrue(archive.exists(), f'{name} must live under docs/history')
                stub = (ROOT/'docs'/name).read_text()
                self.assertIn(f'](history/{name})', stub)
                self.assertLessEqual(len(stub.split()), 50, 'old path must only forward')
        for archive in (ROOT/'docs/history').glob('*.md'):
            with self.subTest(archive=archive.name):
                introduction = '\n'.join(archive.read_text().splitlines()[:10])
                self.assertRegex(introduction, r'\b20\d\d-\d\d-\d\d\b')
                self.assertRegex(introduction.lower(), r'historical|archived|design record')

    def test_managed_marker_matches_installer_and_docs(self):
        template = (ROOT/'scripts/local/instructions.md').read_text()
        marker = re.match(r'<!-- comitia-local:start v(\d+) -->', template)
        self.assertIsNotNone(marker)
        self.assertIsNotNone(comitia_install.BLOCK.fullmatch(template))
        documented = re.findall(r'comitia-local:start v(\d+)', (ROOT/'docs/local-service.md').read_text())
        self.assertTrue(documented)
        self.assertEqual(set(documented), {marker[1]}, 'template/documented marker versions disagree')
        rendered = comitia_install.render_instructions('fixture', {'display': 'Fixture'},
                    {'context_id': 'fixture-context', 'server_url': 'http://localhost:8390'}, Path('/fixture'))
        self.assertNotIn('${', rendered)
        self.assertTrue(rendered.startswith(marker[0]))
        self.assertIsNotNone(comitia_install.BLOCK.fullmatch(rendered))
        old = '<!-- comitia-local:start v1 -->\nold\n<!-- comitia-local:end -->\n'
        self.assertEqual(comitia_install.place_block(old + '# Keep\n', rendered), rendered + '# Keep\n')

    def test_managed_modes_are_exclusive(self):
        template = (ROOT/'skills/comitia/references/coordinate.md').read_text()
        modes = re.findall(r'^- \*\*([^*]+):\*\*', template, re.M)
        self.assertEqual(modes, ['Default', 'Otherwise explicit', 'Legacy'])
        self.assertEqual(template.count('## Discover and choose one instance mode'), 1)
        block = (ROOT/'scripts/local/instructions.md').read_text()
        self.assertNotIn('**Default:**', block)
        self.assertIn('${skill_path}', block)

    def test_skill_is_short_and_routes_to_portable_references(self):
        skill = ROOT/'skills/comitia'
        paths = [skill/'SKILL.md', *sorted((skill/'references').glob('*.md'))]
        self.assertLess(len((skill/'SKILL.md').read_text().splitlines()), 80)
        self.assertLess(sum(len(p.read_text().splitlines()) for p in paths), 500)
        body = (skill/'SKILL.md').read_text()
        self.assertRegex(body, r'^---\nname: comitia\ndescription: .+\n')
        for reference in ('setup', 'coordinate', 'orchestrate'):
            self.assertIn(f'](references/{reference}.md)', body)
        self.assertIn('Set up Comitia from https://github.com/krimvp/comitia: follow skills/comitia/SKILL.md.', body)

    def test_no_brittle_counts_or_absolute_diagnoses(self):
        for path in active_files():
            with self.subTest(doc=path.name):
                text = prose(path.read_text())
                self.assertNotRegex(text, r'(?i)\b(?:credential|token)s?[^\n]{0,100}\b\d+ chars?\b|\b\d+ tools\b')
                self.assertNotRegex(text.lower(), r'if tools do not appear[^\n]*login is expired|the login on this box is currently expired')

    def test_active_word_budgets(self):
        counts = {str(p.relative_to(ROOT)): len(p.read_text().split()) for p in active_files()}
        self.assertEqual(set(counts), set(WORD_BASELINE), 'assign an owner and review a budget for new docs')
        for name, baseline in WORD_BASELINE.items():
            with self.subTest(doc=name):
                self.assertLessEqual(counts[name], baseline * 1.10, 'active guide grew past its reviewed +10% budget')
        self.assertLessEqual(sum(counts.values()), sum(WORD_BASELINE.values()) * 1.10)

    def test_merged_workflows_are_linked(self):
        using = (ROOT/'docs/USING-COMITIA.md').read_text()
        self.assertIn('](task-validation.md#authoritative-events-and-terminal-outcomes)', using)
        self.assertIn('](message-protocol.md#authoritative-task-events)', using)
        self.assertNotIn('issues/63', using)
        for anchor in ('process-then-acknowledge', 'scoped-conversations-and-durable-requests',
                       'exact-sha-certificates-and-merge-reconciliation',
                       'supervised-process-registry-and-steering'):
            self.assertIn(f'](durable-coordination.md#{anchor})', using)
        self.assertNotIn('issues/65', using)
        self.assertNotIn('Placeholder:', using)


class LinkAuditHelpers(unittest.TestCase):
    def test_github_headings_and_fenced_examples(self):
        self.assertEqual(heading_anchors('# A `field` & [link](somewhere)\n# A `field` & [link](somewhere)\n```sh\n# Not a heading\n```\n'),
                         {'a-field--link', 'a-field--link-1'})

    def test_missing_targets_and_anchors_are_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root/'target.md').write_text('# Valid heading\n')
            source = root/'README.md'
            source.write_text('[ok](target.md#valid-heading)\n[bad](target.md#absent)\n![image](absent.svg)\n[ref][r]\n[r]: target.md#valid-heading\n')
            self.assertEqual(len(link_errors(root, source)), 2)
            source.write_text('<a href="target.html#absent">bad</a>')
            (root/'target.html').write_text('<main id="valid"></main>')
            self.assertEqual(len(link_errors(root, source)), 1)


if __name__ == '__main__':
    unittest.main()
