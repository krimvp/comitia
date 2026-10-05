"""Instance forwarding and skill warnings never touch the user's real home."""
import contextlib
import io
import json
import tomllib
from unittest import mock

from scripts.test_install_linux_service import Base, installer, local


class ErgonomicsTest(Base):
    variables = ['COMITIA_AGENT_INSTANCE', 'COMITIA_AGENT_INSTANCE_LABEL']

    def config(self):
        return tomllib.loads((self.home/'.codex/config.toml').read_text())['mcp_servers']['comitia']

    def test_fresh_install_forwards_both_instance_variables(self):
        self.install()
        self.assertEqual(self.config()['env_vars'], self.variables)
        self.assertNotIn('env', self.config())
        claude = json.loads((self.home/'.claude.json').read_text())['mcpServers']['comitia']
        self.assertNotIn('env_vars', claude)

    def test_reinstall_and_add_harness_preserve_user_environment(self):
        self.install()
        path = self.home/'.codex/config.toml'
        text = path.read_text().replace('env_vars = '+local.toml_value(self.variables),
                                       '"env_vars" = [\n "USER_VALUE", # user forwarding\n "COMITIA_AGENT_INSTANCE",\n]')
        path.write_text(text+'\n[mcp_servers.comitia.env]\nCOMITIA_AGENT_INSTANCE = "explicit-worker"\nUSER_SETTING = "keep"\n')
        # Simulate the registry from before forwarding was supported.
        registry = self.root/'harnesses.json'
        table = json.loads(registry.read_text())
        del table['codex']['env_vars']
        registry.write_text(json.dumps(table))
        self.install()
        args = installer.parser().parse_args(['add-harness', 'codex'])
        installer.connect_harness(args, self.root, self.home)
        self.assertEqual(self.config()['env_vars'], ['USER_VALUE', *self.variables])
        self.assertEqual(self.config()['env'], {'COMITIA_AGENT_INSTANCE': 'explicit-worker', 'USER_SETTING': 'keep'})

    def test_invalid_forwarding_is_not_overwritten(self):
        self.install()
        path = self.home/'.codex/config.toml'
        path.write_text(path.read_text().replace(local.toml_value(self.variables), '"bad"'))
        before = path.read_bytes()
        with self.assertRaisesRegex(ValueError, 'env_vars must be an array'):
            self.install()
        self.assertEqual(path.read_bytes(), before)

    def test_forwarding_without_toml_parser_leaves_config_untouched(self):
        self.install()
        path = self.home/'.codex/config.toml'
        before = path.read_bytes()
        with mock.patch.object(local, 'tomllib', None):
            with self.assertRaisesRegex(RuntimeError, 'Python 3.11'):
                self.install()
        self.assertEqual(path.read_bytes(), before)

    def test_install_and_add_warn_once_per_other_skill_path(self):
        paths = []
        for directory in ('.agents/skills', '.codex/skills', '.claude/skills'):
            for name, text in (
                    ('comitia-orchestrate', 'secret body'),
                    ('other', '---\nname: other\ndescription: Coordinate Comitia agents\n---\nsecret body'),
                    ('both', '---\nname: comitia-helper\n---\nsecret body'),
                    ('unrelated', '---\nname: unrelated\n---\nComitia body only'),
                    ('unterminated', '---\ndescription: Comitia\n')):
                path = self.home/directory/name
                path.mkdir(parents=True)
                (path/'SKILL.md').write_text(text)
                paths.append(path)
        before = {path: local.skill_files(path) for path in paths}
        for action in (self.install, lambda: installer.connect_harness(
                installer.parser().parse_args(['add-harness', 'codex']), self.root, self.home)):
            output = io.StringIO()
            with contextlib.redirect_stdout(output):
                action()
            warnings = [line for line in output.getvalue().splitlines() if line.startswith('Warning:')]
            directories = ['.agents/skills', '.codex/skills']
            if action == self.install:
                directories.append('.claude/skills')
            expected = {f'Warning: potentially shadowing Comitia skill: {self.home/directory/name}'
                        for directory in directories for name in ('comitia-orchestrate', 'other', 'both')}
            self.assertEqual(set(warnings), expected)
            self.assertEqual(len(warnings), len(expected))
            self.assertNotIn('secret body', output.getvalue())
            self.assertEqual({path: local.skill_files(path) for path in paths}, before)

    def test_custom_directory_and_duplicate_scan_paths(self):
        directory = self.home/'custom/skills'
        sibling = directory/'comitia-old'
        sibling.mkdir(parents=True)
        spec = {'skills_dir': str(directory), 'other_skills_dirs': [str(directory)]}
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            local.warn_shadow_skills(spec, self.home)
        self.assertEqual(output.getvalue(), f'Warning: potentially shadowing Comitia skill: {sibling}\n')
