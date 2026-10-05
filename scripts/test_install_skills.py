"""Managed skill installs use only temporary homes and source trees."""
import contextlib
import io
import json
from pathlib import Path
import shutil
import tempfile
import unittest
from unittest import mock

from scripts.test_install_linux_service import Base, installer, local


class SkillInstallTest(Base):
    def test_builtins_and_fallback_are_complete_managed_copies(self):
        self.install()
        manifest = json.loads((self.root/'managed-skills.json').read_text())
        expected = local.skill_files(local.REPO/'skills/comitia')
        for directory in (self.home/'.agents/skills/comitia', self.home/'.claude/skills/comitia',
                          self.root/'skills/comitia'):
            self.assertEqual(local.skill_files(directory), expected)
            self.assertEqual(manifest[str(directory)], {'version': 1, 'files': expected})
        self.assertFalse((self.home/'.codex/skills').exists())
        self.assertEqual((self.root/'managed-skills.json').stat().st_mode & 0o777, 0o600)

    def test_no_harness_loader_still_installs_fallback(self):
        self.install(names=[])
        self.assertTrue((self.root/'skills/comitia/SKILL.md').exists())
        self.assertFalse((self.home/'.agents').exists())

    def test_system_temp_alias_above_home_does_not_block_install(self):
        alias = Path(self.tmp.name)/'alias'
        alias.symlink_to(self.home, target_is_directory=True)
        with contextlib.redirect_stdout(io.StringIO()):
            installer.install(alias/'.local/share/comitia', alias, names=['codex'], binary=self.fake,
                              service=None, check_port=False)
        self.assertTrue((self.home/'.agents/skills/comitia/SKILL.md').exists())

    def test_upgrade_replaces_unchanged_tree_and_backs_up_removed_files(self):
        self.install()
        source_repo = Path(self.tmp.name)/'source'
        source = source_repo/'skills/comitia'
        shutil.copytree(local.REPO/'skills/comitia', source)
        (source/'SKILL.md').write_text('---\nname: comitia\ndescription: Updated.\n---\nVersion two.\n')
        (source/'references/coordinate.md').unlink()
        (source/'references/new.md').write_text('New reference.\n')
        directory = self.home/'.agents/skills/comitia'
        with mock.patch.object(local, 'REPO', source_repo), mock.patch.object(local, 'SKILL_VERSION', 2):
            self.assertTrue(local.install_skill(self.root, directory))
            self.assertEqual(local.skill_files(directory), local.skill_files(source))
            manifest = json.loads((self.root/'managed-skills.json').read_text())
            self.assertEqual(manifest[str(directory)]['version'], 2)
            backups = list((self.root/'backups').glob('skill-*'))
            self.assertEqual(len(backups), 1)
            self.assertTrue((backups[0]/'references/coordinate.md').exists())
            self.assertTrue(local.install_skill(self.root, directory))
            self.assertEqual(len(list((self.root/'backups').glob('skill-*'))), 1)

    def test_modified_or_extended_managed_tree_is_preserved(self):
        for change in ('edit', 'delete', 'extra-file', 'extra-directory'):
            with self.subTest(change=change):
                directory = self.home/change/'comitia'
                self.root.mkdir(parents=True, exist_ok=True)
                local.install_skill(self.root, directory)
                if change == 'edit':
                    (directory/'SKILL.md').write_text('My custom instructions.\n')
                elif change == 'delete':
                    (directory/'references/setup.md').unlink()
                elif change == 'extra-file':
                    (directory/'mine.md').write_text('My file.\n')
                else:
                    (directory/'mine').mkdir()
                before = local.skill_files(directory)
                with contextlib.redirect_stdout(io.StringIO()) as output:
                    self.assertFalse(local.install_skill(self.root, directory))
                self.assertIn('Preserved', output.getvalue())
                self.assertEqual(local.skill_files(directory), before)

    def test_unowned_copy_even_identical_is_not_adopted(self):
        directory = self.home/'.agents/skills/comitia'
        shutil.copytree(local.REPO/'skills/comitia', directory)
        before = local.skill_files(directory)
        self.install()
        self.assertEqual(local.skill_files(directory), before)
        manifest = json.loads((self.root/'managed-skills.json').read_text())
        self.assertNotIn(str(directory), manifest)
        local.remove_harnesses(self.root, self.home)
        self.assertEqual(local.skill_files(directory), before)

    def test_symlink_destination_parent_and_contents_are_preserved(self):
        external = self.home/'external'
        shutil.copytree(local.REPO/'skills/comitia', external)
        self.root.mkdir(parents=True)
        direct = self.home/'comitia'
        direct.symlink_to(external, target_is_directory=True)
        parent = self.home/'linked'
        parent.symlink_to(self.home, target_is_directory=True)
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertFalse(local.install_skill(self.root, direct))
            self.assertFalse(local.install_skill(self.root, parent/'comitia'))
        self.assertTrue(direct.is_symlink())
        self.assertEqual(local.skill_files(external), local.skill_files(local.REPO/'skills/comitia'))
        direct.unlink()
        local.install_skill(self.root, direct)
        (direct/'SKILL.md').unlink()
        (direct/'SKILL.md').symlink_to(external/'SKILL.md')
        with contextlib.redirect_stdout(io.StringIO()):
            self.assertFalse(local.install_skill(self.root, direct))
        self.assertIn(str(direct), local.remove_skills(self.root))
        self.assertTrue((direct/'SKILL.md').is_symlink())

    def test_uninstall_removes_matching_copies_retains_edits_and_is_repeatable(self):
        self.install()
        edited = self.home/'.claude/skills/comitia'
        (edited/'SKILL.md').write_text('Mine.\n')
        retained = local.remove_harnesses(self.root, self.home)
        self.assertIn(str(edited), retained)
        self.assertFalse((self.home/'.agents/skills/comitia').exists())
        self.assertFalse((self.root/'skills/comitia').exists())
        self.assertEqual((edited/'SKILL.md').read_text(), 'Mine.\n')
        self.assertEqual(local.remove_harnesses(self.root, self.home), [str(edited)])

    def test_skills_dir_override_survives_reinstall(self):
        self.install()
        custom = self.home/'custom-skills'
        args = installer.parser().parse_args(['add-harness', 'codex', '--skills-dir', str(custom)])
        installer.connect_harness(args, self.root, self.home)
        self.assertTrue((custom/'comitia/SKILL.md').exists())
        spec = local.harnesses(self.root)['codex']
        self.assertEqual(spec['skills_dir'], str(custom))
        self.install()
        self.assertEqual(local.skill_files(custom/'comitia'), local.skill_files(local.REPO/'skills/comitia'))
        self.assertEqual(local.remove_harnesses(self.root, self.home), [])
        self.assertFalse((custom/'comitia').exists())
        self.assertFalse((self.home/'.agents/skills/comitia').exists())

    def test_custom_harness_can_have_no_loader_or_explicit_skills_dir(self):
        self.install()
        for name, skills in (('manual', []), ('loader', ['--skills-dir', str(self.home/'loader/skills')])):
            args = installer.parser().parse_args(['add-harness', name, '--instructions', str(self.home/name/'AGENTS.md'),
                '--mcp-config', str(self.home/name/'mcp.json'), '--no-approval', *skills])
            installer.connect_harness(args, self.root, self.home)
            self.assertEqual('skills_dir' in local.harnesses(self.root)[name], bool(skills))
            self.assertIn(str(self.root/'skills/comitia/SKILL.md'), (self.home/name/'AGENTS.md').read_text())
        self.assertFalse((self.home/'manual/skills').exists())
        self.assertTrue((self.home/'loader/skills/comitia/SKILL.md').exists())

    def test_v4_blocks_upgrade_to_single_short_v5_without_losing_user_rules(self):
        self.install()
        path = self.home/'.codex/AGENTS.md'
        old = '<!-- comitia-local:start v4 -->\nold compatibility\n<!-- comitia-local:end -->\n'
        path.write_text('# Before\n'+old+'# After\n'+old)
        self.install()
        text = path.read_text()
        self.assertEqual(text.count('comitia-local:start'), 1)
        self.assertIn('comitia-local:start v5', text)
        self.assertIn('# Before\n', text)
        self.assertIn('# After\n', text)
        self.assertNotIn('old compatibility', text)
        self.assertLess(len(local.BLOCK.search(text)[0].splitlines()), 20)

    def test_pre_skill_registry_gains_builtin_loader_without_losing_custom_settings(self):
        self.install()
        registry = self.root/'harnesses.json'
        table = json.loads(registry.read_text())
        for name in ('codex', 'claude'):
            del table[name]['skills_dir']
        table['codex']['instructions'] = str(self.home/'custom/AGENTS.md')
        del table['codex']['approval']
        registry.write_text(json.dumps(table))
        for directory in (self.home/'.agents/skills/comitia', self.home/'.claude/skills/comitia'):
            shutil.rmtree(directory)
        self.install()
        self.assertTrue((self.home/'.agents/skills/comitia/SKILL.md').exists())
        self.assertTrue((self.home/'.claude/skills/comitia/SKILL.md').exists())
        self.assertIn('comitia-local:start v5', (self.home/'custom/AGENTS.md').read_text())
        self.assertEqual(local.harnesses(self.root)['codex']['instructions'], str(self.home/'custom/AGENTS.md'))
        self.assertNotIn('approval', local.harnesses(self.root)['codex'])


if __name__ == '__main__':
    unittest.main()
