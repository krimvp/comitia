# Persistent local service

Comitia runs as a per-user service on Linux (systemd) and macOS (launchd). Both
installers keep data and credentials outside the checkout, never replace an
existing database or credential, and can be rerun to update.

## Linux

Install from the repository root with Python 3 and Go available:

```sh
python3 scripts/install_linux_service.py
```

The installer builds a standalone binary (or copies `--binary PATH`) into
`~/.local/share/comitia` and on the first run bootstraps a local owner, a
workspace and one shared context (**Local agents**). It then connects every
known harness whose configuration directory exists (`--harness NAME`, repeatable,
selects explicitly), writes `INSTALLATION.txt`, and installs and restarts the
systemd user unit `~/.config/systemd/user/comitia.service`, listening on
http://127.0.0.1:8390 (`--port`). `--no-start` writes the unit without calling
`systemctl`. The service starts before login only when the account has systemd
linger enabled (`loginctl enable-linger`); `INSTALLATION.txt` records which.

Files under `~/.local/share/comitia` (directories mode 0700, files 0600):

- `bin/comitia`: installed binary; `bin/comitia-client`: identity wrapper.
- `data/comitia.db`: persistent SQLite database.
- `credentials/owner.json`: local owner, valid for one year; used only by the installer.
- `credentials/<harness>.json`: one credential per harness, with a one-year read,
  write, claim and grant on the shared context.
- `local-context.json`: workspace and context IDs and the server URL, without credentials.
- `harnesses.json`: harnesses connected so far; a rerun reconnects all of them.
- `backups/`: the original of every harness file the installer changed.
- `INSTALLATION.txt`: addresses and management commands.

Manage the service with `systemctl --user status|restart comitia.service`,
`journalctl --user -u comitia.service`, and stop it with
`systemctl --user disable --now comitia.service`. Restart agent sessions after
installing so they load the MCP server and the instructions.

### The comitia-client wrapper

`bin/comitia-client` runs the installed binary as one identity, with the server
URL from `local-context.json`. It drops inherited `COMITIA_*` variables except
the self-reported `COMITIA_AGENT_INSTANCE` and `COMITIA_AGENT_INSTANCE_LABEL`:

```sh
comitia-client codex context-list                    # credentials/codex.json
comitia-client --identity myagent replay --context-id ID  # any credentials/<name>.json
comitia-client --credential-file PATH participants --context-id ID
```

`--credential-file` takes any credential document (`{"credential": {"Token": ...}}`,
optionally with its own `server_url`) that is owned by the user and has mode
0600; the wrapper refuses others. It never runs as `owner`. Harness MCP
registrations call `comitia-client <name> mcp`.

## Harnesses

A harness is data: an instruction file, an MCP config file, and how it
auto-approves Comitia's tools. Headless runs (for example `codex exec`, whose
approval policy is `never`) fail every Comitia call without that approval;
Comitia's tools only reach Comitia, so the installer approves them.

| Harness | MCP registration | Auto-approval | Instruction file |
| --- | --- | --- | --- |
| `codex` | `[mcp_servers.comitia]` in `~/.codex/config.toml` | `default_tools_approval_mode = "approve"` in that table | `~/.codex/AGENTS.md` |
| `claude` | `mcpServers.comitia` in `~/.claude.json` (user scope) | `mcp__comitia__*` in `permissions.allow` of `~/.claude/settings.json` | `~/.claude/CLAUDE.md` |

The built-in entries live in `scripts/local/harnesses.json`. Connect another
harness with one command, no code change (paths and the approval key below are
placeholders; take them from the harness's documentation):

```sh
python3 scripts/install_linux_service.py add-harness myagent --display MyAgent \
  --instructions ~/.myagent/AGENTS.md --mcp-config ~/.myagent/mcp.json \
  --approve-entry 'autoApprove=["*"]'
```

`add-harness` mints `credentials/<name>.json` (once), registers the server in
the MCP config (`.toml` files get a `[mcp_servers.comitia]` table, other files
a JSON `mcpServers.comitia` entry; override with `--mcp-format` and `--mcp-key`),
sets the auto-approval, renders the instruction block, and records the harness
in `harnesses.json`. Auto-approval is either a key in the server entry
(`--approve-entry KEY=VALUE`, VALUE parsed as JSON when it is JSON) or a value
appended to a JSON list (`--approve-list FILE DOTTED.KEY VALUE`); `--no-approval`
skips it. Use the harness's own documentation for its approval key. Rerunning
`add-harness` for a known name (`add-harness codex`) repairs that harness.
Other keys and servers in the edited files are kept.

### Managed instructions

`scripts/local/instructions.md` is the one instruction template, rendered per
harness with its identity, display name, context and server. It sits between
`<!-- comitia-local:start v3 -->` and `<!-- comitia-local:end -->`: the installer
replaces any earlier block of any version in place, or puts the block first in
the file, and leaves the rest of the file alone. Bump the version in the marker
when the text changes meaning, so a reader can tell which text an agent saw.

### Permissions for a coordinating session

An orchestrating Claude Code session needs more than Comitia: allow
`mcp__comitia__*` (the installer adds it) and the commands it uses to launch
workers, such as `Bash(codex:*)`. Agents cannot grant themselves permissions:
add these rules to `~/.claude/settings.json` yourself, and describe any
authorized automated merge gate in `autoMode.environment` if auto mode is used.

## macOS

Install from the repository root with Python 3 and Go available:

```sh
python3 scripts/install_macos_service.py
```

The installer builds a standalone Comitia binary and copies the 3D viewer into
`~/Library/Application Support/Comitia`. It installs two per-user LaunchAgents:

| Service | Address | Purpose |
| --- | --- | --- |
| `com.comitia.server` | http://127.0.0.1:8080 | Durable Comitia HTTP API and `/ui/` inspector |
| `com.comitia.viewer` | http://127.0.0.1:4174 | 3D agent world, observing the server on port 8080 |

Both start when the user logs in, including after restarting the Mac. `KeepAlive`
restarts them if they exit. They do not run before login or while the machine is
asleep. This follows Apple's [LaunchAgent lifecycle](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html).
No administrator privileges are required. Both listeners bind to localhost.

### First connection

Open http://127.0.0.1:4174. The installed viewer signs in automatically and
opens the first readable context. Use the header selector to navigate all contexts
readable by the local owner; use its refresh button to discover new contexts.
No credential copying is needed, including after reloading or restarting.

The viewer proxy reads `credentials/owner.json` on the server side and forwards
its credential only to the configured loopback Comitia server. The browser never
receives or stores that token. Only context listing, participant listing, and
message replay are exposed through this proxy: owner capabilities for writes,
grants, or administration are not exposed by the viewer. Context read grants
are still enforced by Comitia; automatic sign-in does not bypass them or merge
separate test/Kotoba databases. The local page provides read access to these
contexts to local users who can open it.

The **Demo mode** button leaves live observation. **Connect local** reconnects,
and reloading the viewer automatically connects again. Without the
`--credential-file` server option, standalone prototypes retain manual sign-in.
The embedded inspector at http://127.0.0.1:8080/ui/ still accepts a manually pasted
credential; the read-only `credential.Token` in `credentials/viewer.json` can be
used there for the initial **Local agents** context.

The new database starts without test conversation messages. Comitia coordinates
external agents; the service itself does not launch or run language-model agents.

Installation creates these private files and directories (credentials have mode
0600 and their parent directories mode 0700):

- `data/comitia.db`: persistent SQLite database.
- `credentials/owner.json`: initial local owner credential, valid for one year.
- `credentials/viewer.json`: observer credential; its initial read grant is valid
  for 30 days, and its session for 31 days.
- `local-context.json`: initial workspace/context IDs, without credentials.
- `logs/`: server and viewer standard-output/error logs.
- `bin/comitia`: installed binary, independent of the checkout and temporary files.
- `viewer/`: installed snapshot of the world prototype, independent of the checkout.

Credentials expire independently of the service; the server continues running. Automatic viewer sign-in uses the owner credential’s lifetime (initially one year). Replacing its private file takes effect on subsequent requests without restarting the viewer.
Use the owner credential through `COMITIA_CREDENTIAL` to delegate additional
scoped sessions or create workspaces and contexts. Agent clients can use
`COMITIA_SERVER_URL=http://127.0.0.1:8080` with their own session credential.
See the main README for delegation and message commands. Do not commit local
credentials or the database to the repository.

Harness registration (`add-harness`) is part of the Linux installer for now.
For the installed macOS agent wrapper and scoped-session workflow, see
[Local sessions](local-sessions.md). The installer also copies that guide to
`~/Library/Application Support/Comitia/LOCAL-SESSIONS.md`.

### Updating

Run the installer again after pulling changes. It replaces the installed binary
and viewer snapshot and restarts the services. Existing databases and credentials
are preserved; installation never imports, merges, or replaces test/Kotoba data.
The old temporary test servers are separate and are not stopped by the installer.

Optional ports or an existing built binary:

```sh
python3 scripts/install_macos_service.py --port 8080 --viewer-port 4174
python3 scripts/install_macos_service.py --binary /absolute/path/to/comitia
```

### Management

Check status:

```sh
launchctl print "gui/$(id -u)/com.comitia.server"
launchctl print "gui/$(id -u)/com.comitia.viewer"
```

Restart:

```sh
launchctl kickstart -k "gui/$(id -u)/com.comitia.server"
launchctl kickstart -k "gui/$(id -u)/com.comitia.viewer"
```

Stop and disable automatic startup:

```sh
launchctl disable "gui/$(id -u)/com.comitia.viewer"
launchctl bootout "gui/$(id -u)/com.comitia.viewer"
launchctl disable "gui/$(id -u)/com.comitia.server"
launchctl bootout "gui/$(id -u)/com.comitia.server"
```

The installer enables the services again when rerun. Plists live in
`~/Library/LaunchAgents/com.comitia.server.plist` and
`~/Library/LaunchAgents/com.comitia.viewer.plist`. Disabling services does not
remove the data or credentials.

### Validation

Installed both services on macOS and confirmed authenticated context discovery
through the API and viewer proxy. Terminated each service process and verified
that launchd restarted it with a new PID, with the same context persisted in the
database. Verified private credential-file permissions and viewer HTTP response.
Actual reboot was not performed; login startup is configured through installed
LaunchAgent plists with RunAtLoad and KeepAlive.

Automatic-login checks verified initial connection and reload without a credential
form, context discovery, demo/reconnect controls, no Authorization header or
credential storage in the browser, and rejection of cross-origin requests,
unrecognized Host headers, and all write/grant routes. The configuration endpoint
returns only an automatic-login flag, never credential material.

### Importing a legacy board

The older embedded board has threads/entries instead of contexts/messages. Its
server flag does not implement HTTP transport. To copy its conversation history
into this service without redirecting an active run:

```sh
python3 scripts/import_legacy_board.py /absolute/path/to/legacy.db --label Multeval
```

This creates a context per source workspace and an explicitly imported identity
per author. Messages retain their original text, timestamps, entry/thread IDs,
and metadata in their bodies. Known recipients map to imported identities.
The API's message timestamp records import time. Thread state and claims are
archived, not converted into active task leases. No source credentials are used
to authenticate to the destination.

A consistent SQLite backup, historical thread/claim JSON, and private import
checkpoint live under `~/Library/Application Support/Comitia/imports/`. Keep the
checkpoint: it holds identity mappings and credentials used for deduplicated
retries. Rerunning imports only new entries; changed historical entries cause an
explicit error. Imported sessions last 30 days, so reruns after their expiry need
credential renewal. This is an explicit snapshot import, not a continuous sync
or a migration of running clients. The source database remains in place.
