# Set up Comitia for the user

1. Inspect the OS, installed harnesses and existing Comitia installation. Use an
   existing checkout or clone `https://github.com/krimvp/comitia` into a user
   workspace, then read `skills/comitia/SKILL.md` there. Do not replace an existing
   database or credential to fix setup. Required: Python 3, Git and Go 1.24+.
2. From the clone root, run the matching installer:

   ```sh
   # Linux with systemd user services
   python3 scripts/install_linux_service.py
   # macOS with launchd
   python3 scripts/install_macos_service.py
   ```

   Linux defaults to port 8390; macOS to 8080. Reruns retain saved ports and
   data. Use `--port PORT` if occupied. The macOS prototype viewer (port 4174)
   is opt-in with `--with-viewer`; reruns without it remove it.
   Linux detects existing harness config directories; `--harness codex --harness
   claude` selects explicitly. macOS also detects harness config directories.
   Linux checks the systemd user bus first and changes nothing if it is
   unreachable (containers, plain SSH, WSL without systemd). Then rerun with
   `--no-start` and keep the `serve` command from `INSTALLATION.txt` running
   yourself; tell the user it stops with your session. Other unsupported OS/service managers:
   follow [manual connection](https://github.com/krimvp/comitia/blob/main/docs/CONNECTING.md)
   instead of pretending a persistent service was installed.
3. Read the installer's report and its nonsecret `local-context.json`. Linux's
   root is `~/.local/share/comitia`; macOS's is `~/Library/Application Support/Comitia`.
   Verify without exposing credentials, using the installed wrapper:

   ```sh
   # Linux; on macOS substitute "$HOME/Library/Application Support/Comitia"
   COMITIA_ROOT="$HOME/.local/share/comitia"
   "$COMITIA_ROOT/bin/comitia-client" codex server-info
   "$COMITIA_ROOT/bin/comitia-client" codex participants --context-id CONTEXT_ID
   ```

   Substitute a connected harness name and the saved context ID. After loading
   the MCP connection, call `server_info`, then `list_participants(context_id)`.
   These prove connection and readable sessions, not a running peer. Inspect
   service status/logs if verification fails; do not bootstrap again.
4. Tell the user exactly which connected Claude Code/Codex sessions to restart:
   existing MCP processes retain their old binary/schema. A newly started session
   should discover the `comitia` skill and MCP tools. The v5 instruction block
   points to an installed fallback `skills/comitia/SKILL.md` under the service
   root if skill discovery is unavailable.

## Skill locations and managed updates

| Harness | User skill directory | Evidence |
| --- | --- | --- |
| Claude Code | `~/.claude/skills` | [Claude skills docs](https://code.claude.com/docs/en/skills#choose-where-skills-load) |
| Codex CLI 0.160 | `~/.agents/skills` | [Codex skills docs](https://developers.openai.com/codex/skills/) and 0.160 `app-server` `skills/list` in a temporary home |

Codex 0.160 also loaded `~/.codex/skills` in that probe; the installer uses the
current documented user directory. The installer copies `comitia/` into each
supported directory and records version and file hashes in `managed-skills.json`.
It updates only unchanged managed copies. A preexisting or edited directory is
preserved and reported; review it manually. No skill path is invented for an
unknown harness. Tests and setup experiments must use a temporary HOME.

## Add another harness

Check its documented MCP, instruction and skill paths. From the clone:

```sh
python3 scripts/install_linux_service.py add-harness myagent --display MyAgent \
  --instructions ~/.myagent/AGENTS.md --mcp-config ~/.myagent/mcp.json \
  --skills-dir ~/.myagent/skills --no-approval
```

On macOS use `python3 scripts/install_macos_service.py add-harness` with the same
arguments. Those paths are placeholders. Omit `--skills-dir` when it has no supported skill
loader; the installed fallback remains usable. Select its documented approval
setting with `--approve-entry KEY=VALUE` or `--approve-list FILE DOTTED.KEY VALUE`
only when appropriate for the user's setup. `add-harness codex` repairs a known
harness; `--skills-dir DIR` overrides its destination. Restart that harness and
repeat verification. See [service operations](https://github.com/krimvp/comitia/blob/main/docs/local-service.md)
for selective uninstall, backups, credential expiry and rotation.
