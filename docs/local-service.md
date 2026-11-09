# Persistent local service

Comitia runs as a per-user service on Linux (systemd) and macOS (launchd). Both
installers keep data and credentials outside the checkout, never replace an
existing database or credential, and can be rerun to update.

For private online snapshots, matching credentials/config, and an isolated
authentication/cursor/task recovery drill, follow [backup and restore
reconciliation](backup-restore.md). Fence workers and reconcile rolled-back
revocations, leases and external effects before resuming a restored service.

## Linux

Install from the repository root with Python 3 and Go 1.24 or newer available:

```sh
python3 scripts/install_linux_service.py
```

The installer builds a standalone binary (or copies `--binary PATH`) into
`~/.local/share/comitia` and on the first run bootstraps a local owner, a
workspace and one shared context (**Local agents**). It then connects every
known harness whose configuration directory exists (`--harness NAME`, repeatable,
selects explicitly), writes `INSTALLATION.txt`, and installs and restarts the
systemd user unit `~/.config/systemd/user/comitia.service`, listening on
http://127.0.0.1:8390 on the first install (`--port`). Rerunning without `--port`
keeps the URL saved in `local-context.json`. Fresh or changed ports must be free
before the installer changes binaries, credentials, harness settings or the unit.
An unchanged port is exempt only when the saved URL, unit and running process's
executable and arguments match this installation and port. A saved or rewritten
unit alone cannot bypass the check after an interrupted port change.
`--no-start` writes the unit without starting it and still checks fresh/changed
ports. Without `--no-start`, the installer exits before changing anything when no
systemd user manager is reachable (containers, WSL without systemd). There,
install with `--no-start` and run the `Without systemd:` command from
`INSTALLATION.txt` yourself; it does not survive logout or reboot. Generated command paths are escaped for systemd, including spaces and
percent specifiers; `/usr/bin/env` execs the installed binary so quotes and
backslashes in its path are also supported. An interrupted first run resumes on the next run: the owner is
bootstrapped into a staged database that moves into place only after
`owner.json` is saved, and `local-context.json` records each created ID. The service starts before login only when the account has systemd
linger enabled (`loginctl enable-linger`); `INSTALLATION.txt` records which.

Files under `~/.local/share/comitia` (directories mode 0700, files 0600):

- `bin/comitia`: installed binary; `bin/comitia-client`: identity wrapper.
- `data/comitia.db`: persistent SQLite database.
- `credentials/owner.json`: local owner, valid for one year and one hour; used only by the installer.
- `credentials/<harness>.json`: one credential per harness, with a read, write,
  claim and grant on the shared context. Session and grant last one year, but
  always end an hour before the owner does (a child cannot outlive its parent);
  `add-harness` reports an owner that is about to expire.
- `local-context.json`: workspace and context IDs and the server URL, without credentials.
- `harnesses.json`: harnesses connected so far; a rerun reconnects all of them.
- `managed-harnesses.json`: records managed harness locations (including previous
  locations and interrupted registrations) and approval-list values added by the
  installer, so uninstall preserves approvals that were already present.
- `backups/`: the original of every harness file the installer changed.
- `INSTALLATION.txt`: addresses and management commands.

Manage the service with `systemctl --user status|restart comitia.service`,
`journalctl --user -u comitia.service`, and stop it with
`systemctl --user disable --now comitia.service`. Restart agent sessions after
installing so they load the MCP server and the instructions. To remove the managed
integration, run `python3 scripts/install_linux_service.py uninstall` from the
checkout. It stops/disables the matching service, removes its generated unit,
reloads systemd, removes matching MCP fields and instruction blocks, and removes
installer-added approval-list values. Custom keys, other MCP servers, instructions
outside the managed block, and replaced command entries are kept.
TOML removal recognizes quoted keys and verifies the entire parsed result before
writing; an edit that changes unrelated configuration is refused. Changed units
are stopped when they still launch this installation but retained for manual
review; unrelated units are left alone. The command is repeatable and uses the
same installation lock as repair and credential rotation. If `systemctl` fails, uninstall still removes the harness integration, keeps
the unit, names the failed command and exits non-zero; stop the service or
your `serve` process, then rerun or delete the unit. Restart agent sessions after removal so existing MCP
clients exit.

Uninstall keeps the entire installation root, including the database, credentials,
delegated sessions and backups. For installations predating approval tracking,
review the registered harness's settings and remove the exact `mcp__comitia__*`
approval (or its configured equivalent) only if it came from Comitia; provenance
is unknown, so uninstall keeps it. Review any retained entries printed by uninstall
before deleting files manually. Data/credential deletion is a separate, optional
step: after stopping clients and keeping any required backup, remove the intended
files under `~/.local/share/comitia/data`, `credentials` and `sessions`, or remove
the whole `~/.local/share/comitia` root when nothing needs to be retained. Deleting
credential files does not revoke sessions; use `revoke` before stopping the daemon
if remote copies must also lose access. Do not restore whole harness backup files
over newer user edits.

### The comitia-client wrapper

`bin/comitia-client` runs the installed binary as one identity, with the server
URL from the selected credential document, falling back to `local-context.json`.
It rejects global `--server-url`, `--db`, `--credential` and
`--bootstrap-credential` overrides, including single-dash and `=VALUE` forms,
to keep the selected credential pinned to its server. It drops inherited
`COMITIA_*` variables except
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

### Spawn flow: a delegated session per task owner

Use shared `codex`/`claude` credentials plus distinct instance IDs for loose
collaboration. Give an agent that owns a task or child context a delegated
session: it becomes a separate participant with its own grants. Instance IDs
alone do not restrict access.

The coordinator calls MCP `delegate_session`, for example:

```json
{"name":"task-15-owner","ttl_seconds":7200,"grants":[{"context_id":"EPIC_CONTEXT_ID","permissions":["read","write","claim","grant","create"],"ttl_seconds":7000}]}
```

The coordinator must already hold a grant that covers those permissions and
lifetimes on the epic. The installed shared harness grants do **not** include
`create`: an owner or authorized grant holder must grant it first. Include
`grant` only if the task owner needs to delegate further. The result contains
`session_id`, `principal_id`, `expires_at`, `grant_ids`, and `credential_file`;
keep these IDs for revocation. The token stays in the exclusive 0600 file.
Delegation does not launch a process.

For the Linux installation below, substitute the returned absolute path for
`SESSION_ID.json`. Pass the epic ID, assigned task and session ID in the helper's
prompt so it uses these instead of the installed shared-context instructions.
Codex overrides the existing Comitia registration for this run:

```sh
codex exec \
  -c 'mcp_servers.comitia.command="/home/alice/.local/share/comitia/bin/comitia-client"' \
  -c 'mcp_servers.comitia.args=["--credential-file","/home/alice/.local/share/comitia/sessions/SESSION_ID.json","mcp"]' \
  -c 'mcp_servers.comitia.default_tools_approval_mode="approve"' \
  'Own task 15 in epic EPIC_CONTEXT_ID as session SESSION_ID. Use your delegated Comitia MCP session; create a private child context for the task and report progress to the epic.'
```

Claude uses an inline MCP JSON config for this run; strict mode avoids loading
another Comitia registration from shared configuration:

```sh
claude --strict-mcp-config \
  --mcp-config '{"mcpServers":{"comitia":{"type":"stdio","command":"/home/alice/.local/share/comitia/bin/comitia-client","args":["--credential-file","/home/alice/.local/share/comitia/sessions/SESSION_ID.json","mcp"]}}}' \
  --allowedTools 'mcp__comitia__*' \
  -p 'Own task 15 in epic EPIC_CONTEXT_ID as session SESSION_ID. Use your delegated Comitia MCP session; create a private child context for the task and report progress to the epic.'
```

The coordinator can call `revoke_session` with `{"session_id":"SESSION_ID"}`
to revoke a directly delegated helper and its descendants, or `revoke_grant`
with `{"grant_id":"GRANT_ID"}` to revoke a grant subtree. The CLI equivalents
are `comitia-client codex revoke --session-id SESSION_ID` and
`comitia-client codex grant-revoke --grant-id GRANT_ID`. Revoking a grant removes
that authority; the session can remain active with other grants. Expiry also
ends access. Stop the helper process separately when its work is finished.

```sh
comitia-client sessions          # JSON: credential_file, session_id, expires_at, status
comitia-client sessions --prune  # remove expired/revoked files; report pruned: true
```

`sessions` is reserved and cannot be used as a harness or identity name.
Listing scans only `<installation>/sessions/*.json`, never shared
`credentials/`. It refuses to use symlinks, non-regular files and credentials not
private to their owner. Unreadable, malformed or incomplete files are reported
as `invalid` and kept; a bad entry does not hide the report of earlier removals. Expiry is checked locally; a server authentication
probe detects revoked sessions (including revoked session ancestors). An
unreachable server or an inconclusive response produces `unknown`, which is
kept by `--prune`. `active` means the session authenticates, not that every grant
is still valid. Tokens are never printed.

The MCP default sessions directory is `~/.local/share/comitia/sessions`. If you
install the wrapper under another root, configure the coordinator's MCP
`mcp --sessions-dir <installation>/sessions` so listing finds its delegated
files. These snippets require the Python `comitia-client` wrapper; the macOS
installer's separate `comitia` wrapper is documented in [Local sessions](local-sessions.md).

## Harnesses

A harness is data: an instruction file, an MCP config file, and how it
auto-approves Comitia's tools. Headless runs (for example `codex exec`, whose
approval policy is `never`) fail every Comitia call without that approval;
Comitia's tools only reach Comitia, so the installer approves them.

| Harness | MCP registration | Auto-approval | Instruction file | Skills directory | Per-launch instance |
| --- | --- | --- | --- | --- | --- |
| `codex` | `[mcp_servers.comitia]` in `~/.codex/config.toml` | `default_tools_approval_mode = "approve"` in that table | `~/.codex/AGENTS.md` | `~/.agents/skills` | `COMITIA_AGENT_INSTANCE=codex-worker COMITIA_AGENT_INSTANCE_LABEL=Worker codex exec ...` |
| `claude` | `mcpServers.comitia` in `~/.claude.json` (user scope) | `mcp__comitia__*` in `permissions.allow` of `~/.claude/settings.json` | `~/.claude/CLAUDE.md` | `~/.claude/skills` | `COMITIA_AGENT_INSTANCE=claude-worker COMITIA_AGENT_INSTANCE_LABEL=Worker claude -p ...` |

Codex CLI 0.160 loads the documented user directory `~/.agents/skills`; a
temporary-home `app-server` `skills/list` probe also verified `~/.codex/skills`.
See [Codex skills](https://developers.openai.com/codex/skills/) and
[Claude Code skills](https://code.claude.com/docs/en/skills#choose-where-skills-load).

With Python 3.11+, the installer registers Codex `env_vars` for `COMITIA_AGENT_INSTANCE` and
`COMITIA_AGENT_INSTANCE_LABEL`, merging existing forwarded names on reinstall.
Explicit server `env` settings are preserved. Codex allowlists the MCP environment;
Claude Code forwards these launch variables directly. For a Codex launch override,
use `-c 'mcp_servers.comitia.env.COMITIA_AGENT_INSTANCE="codex-worker"'`
(and the same syntax for `COMITIA_AGENT_INSTANCE_LABEL`). Restart the harness
after repairing its MCP registration. Use a distinct stable ID per process.

The built-in entries live in `scripts/local/harnesses.json`. Connect another
harness with one command, no code change (paths and the approval key below are
placeholders; take them from the harness's documentation):

```sh
python3 scripts/install_linux_service.py add-harness myagent --display MyAgent \
  --instructions ~/.myagent/AGENTS.md --mcp-config ~/.myagent/mcp.json \
  --skills-dir ~/.myagent/skills \
  --approve-entry 'autoApprove=["*"]'
```

On macOS use `python3 scripts/install_macos_service.py add-harness` with the same
flags. `--skills-dir DIR` copies the skill to `DIR/comitia`; omit it for a harness
without a supported skill loader. Known harnesses retain their default directory.

`add-harness` mints `credentials/<name>.json` (once), registers the server in
the MCP config (`.toml` files get a `[mcp_servers.comitia]` table, other files
a JSON `mcpServers.comitia` entry; override with `--mcp-format` and `--mcp-key`),
sets the auto-approval, renders the instruction block, and records the harness
in `harnesses.json`. Auto-approval is either a key in the server entry
(`--approve-entry KEY=VALUE`, VALUE parsed as JSON when it is JSON) or a value
appended to a JSON list (`--approve-list FILE DOTTED.KEY VALUE`); `--no-approval`
skips it. Use the harness's own documentation for its approval key. Rerunning
`add-harness` for a known name (`add-harness codex`) repairs that harness.
Other keys and servers in the edited files are kept. A TOML edit is parsed back
(Python 3.11+ `tomllib`) and refused, leaving the file untouched, if the file was
not valid TOML or the result would not be.

### Managed skills

The installer copies [the Comitia skill](../skills/comitia/SKILL.md) to each
harness's supported skill directory and to `skills/comitia` under the installation
root as a fallback. `managed-skills.json` records the version and whole-tree hashes.
Reruns replace only unchanged managed copies, backing up the old skill. Unowned,
modified or symlinked directories are preserved and reported. Review them manually;
the installer never adopts a user copy. Uninstall removes matching managed skills
and retains edited copies. Unknown harnesses get only the fallback unless
`--skills-dir` supplies a verified loader directory.

Install and `add-harness` also warn once per other skill path whose directory name
starts with `comitia` or whose `SKILL.md` frontmatter mentions Comitia (case
insensitive). They scan the configured skills directory and Codex's alternate
`~/.codex/skills`, exclude the managed `comitia` directory, and print only paths.
These potentially competing skills are never changed; review them manually.

### Managed instructions

`scripts/local/instructions.md` is the one instruction template, rendered per
harness with its identity, display name, context and server. It sits between
`<!-- comitia-local:start v5 -->` and `<!-- comitia-local:end -->`: the installer
replaces any earlier block of any version in place, or puts the block first in
the file, and leaves the rest of the file alone. Bump the version in the marker
when the text changes meaning, so a reader can tell which text an agent saw.
The short v5 block routes agents to the skill or its installed fallback; the
compatibility and instance procedure lives in
[coordinate](../skills/comitia/references/coordinate.md). Existing v4 blocks
upgrade in place.

### Permissions for a coordinating session

An orchestrating Claude Code session needs more than Comitia: allow
`mcp__comitia__*` (the installer adds it) and the commands it uses to launch
workers, such as `Bash(codex:*)`. Agents cannot grant themselves permissions:
add these rules to `~/.claude/settings.json` yourself, and describe any
authorized automated merge gate in `autoMode.environment` if auto mode is used.

## macOS

Install from the repository root with Python 3 and Go 1.24 or newer available:

```sh
python3 scripts/install_macos_service.py
```

The installer connects registered harnesses and detected built-in config
directories, installing their MCP registration, short instructions and skill.
Restart connected agent sessions after install or upgrade.

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

Run `comitia-viewer` to open http://127.0.0.1:4174 with a private launcher
handoff. No credential copying is needed. The launcher reads a mode-0600 key and
passes it in a URL fragment, which the page immediately removes and exchanges
for an eight-hour viewer session. That session stays in tab storage, scoped to
the viewer origin and port; reloads in the same tab reconnect. Open the launcher
again after session expiry or a viewer service restart. Opening the bare URL
shows the demo/manual connection flow.

The viewer proxy reads `credentials/viewer.json` on the server side and forwards
its read-only credential only to the configured loopback Comitia server. The
browser never receives that Comitia token. The observer initially reads only
**Local agents**; the owner can explicitly grant it read access to additional
contexts. The header selector lists those readable contexts. Automatic proxy
requests require the authenticated viewer session; an unrelated machine-local
HTTP caller cannot borrow the saved credential. Context listing, participant
listing, and message replay are the only proxy operations. Host/Origin/fetch
checks remain enforced, and Comitia checks every context read grant.

The **Demo mode** button leaves live observation. **Connect local** reconnects
within an authenticated viewer session. Without `--credential-file`, standalone
prototypes retain manual sign-in. The embedded inspector at
http://127.0.0.1:8080/ui/ still accepts a manually pasted credential; the read-only
`credential.Token` in `credentials/viewer.json` can be used there for the initial
**Local agents** context.

The new database starts without test conversation messages. Comitia coordinates
external agents; the service itself does not launch or run language-model agents.

Installation creates these private files and directories (credentials have mode
0600 and their parent directories mode 0700):

- `data/comitia.db`: persistent SQLite database.
- `credentials/owner.json`: initial local owner credential, valid for one year.
- `credentials/viewer.json`: observer credential; its initial read grant is valid
  for 30 days, and its session for 31 days.
- `credentials/viewer-launch-key`: private launcher authentication key.
- `local-context.json`: initial workspace/context IDs, without credentials.
- `logs/`: server and viewer standard-output/error logs.
- `bin/comitia`: installed binary, independent of the checkout and temporary files.
- `viewer/`: installed snapshot of the world prototype, independent of the checkout.

Credentials expire independently of the service; the server continues running. Automatic viewer reads use the observer credential’s lifetime and read grants (initially 31 and 30 days). Replacing its private credential file takes effect on subsequent requests without restarting the viewer. Restart the viewer after replacing its launcher key; restarts invalidate existing browser sessions.
Use the owner credential through `COMITIA_CREDENTIAL` to delegate additional
scoped sessions or create workspaces and contexts. Agent clients can use
`COMITIA_SERVER_URL=http://127.0.0.1:8080` with their own session credential.
See [local daemon](local-daemon.md) for delegation and the
[two-session walkthrough](USING-COMITIA.md#two-session-cli-walkthrough) for message
commands. Do not commit local credentials or the database to the repository.

Harness registration (`add-harness`) is part of the Linux installer for now.
For a complete macOS first connection, including creating A, joining B to
the same context, Codex/Claude MCP configs and send/read/claim acceptance, see
[Connect two harnesses](local-sessions.md#connect-two-harnesses). The installer also copies that guide to
`~/Library/Application Support/Comitia/LOCAL-SESSIONS.md`.

### Updating

Run the installer again after pulling changes. It replaces the installed binary
and viewer snapshot and restarts the services. Existing databases and credentials
are preserved; unrelated temporary servers and databases are left alone.
Omitted `--port` and `--viewer-port` keep the saved URLs in `service.json`
(the server also falls back to `local-context.json` for older installs).
Fresh/changed ports are checked before any installation files are rewritten,
even when launchd has the old services loaded; unchanged ports are exempt only
when the saved URLs, plists and running launchd jobs' arguments match the ports.
Rewritten plists from an interrupted update do not prove that the old jobs have
switched ports. The server LaunchAgent overrides inherited
`COMITIA_SERVER_URL`, `COMITIA_CREDENTIAL` and `COMITIA_BOOTSTRAP_CREDENTIAL`
with empty values, while using its configured database and listen arguments.

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
For removal, run `python3 scripts/install_macos_service.py --uninstall`. It
stops/disables the matching services, removes generated Comitia plists (including
viewer plists from before the scoped launcher) and only the
`~/.local/bin/comitia`/`comitia-local`/`comitia-viewer` symlinks that still point
to this installation. Installation refuses to start when one of those commands
already exists for something else. Unrelated commands and plists, plus customized Comitia plists,
are retained and reported for review. It also removes any harness integration
recorded in this root using the same selective rules as Linux. Manually connected
macOS harnesses are outside the installer registry: remove only their Comitia
MCP registration and managed instruction block, keeping other configuration.
If `launchctl` fails, uninstall behaves the same way, keeping the plist.
Restart agent sessions after removal. The command is repeatable and keeps
`~/Library/Application Support/Comitia`, including data, credentials (including
`viewer-launch-key`) and backups.
Apply the same separate data/credential deletion procedure described above to
this root only after reviewing retained integration and keeping required backups.

### Validation

Earlier manual installation and browser observations are preserved in a
[dated archive](history/local-service-validation-20261005.md). Current automated
checks live in [installer tests](../scripts/test_install_linux_service.py) and
[viewer tests](../scripts/test_world_viewer.py); they use isolated fixtures.

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

### Clients still running after an upgrade

Installing a new binary restarts the service, but an agent's existing MCP
process keeps running its previous binary. Restart the agent session after
upgrades. The managed block is `comitia-local:start v5`; its text can update
before the client restarts. Follow [client compatibility](message-protocol.md#compatibility)
and compare the daemon's `server_info.build` with the client's MCP initialize
build identity to distinguish source builds with the same `dev` version.

On Linux, the installer reports PIDs of stale `comitia mcp` processes after
replacing the binary. It compares `/proc/<pid>/exe` device/inode and modification
time with the installed executable, including deleted executables still in use.
The scan is best effort: exited or inaccessible processes are skipped, and only
PIDs are printed, never command arguments or credentials. Other platforms skip
this scan. The installer does not terminate agents; restart their sessions after
reviewing the report.

### Diagnosing write failures

A loaded inspector or successful `server_info` call establishes read/capability
availability. To check writer admission, run `comitia --server-url URL
--credential ADMIN storage-status` (or use `--db PATH` for embedded SQLite).
The command emits JSON and requires an administrator credential; unsupported
servers report a clear error. You can also call `storage_status` through an
administrator's MCP connection or authenticated `POST /v1/storage_status` with an
empty JSON object. Its rolled-back write probe has a 250 ms context budget and returns
`write_ready` plus process-local write failure counts and durations, including the
last failed write's duration. The probe does not test free disk space and does
not guarantee the next write will succeed; see the
[operational protocol](message-protocol.md#operational-failures-and-write-readiness).

`comitia serve` logs one startup line after binding, with its listen address,
version, revision and database path. Enable request logs with `serve
--log-requests` or `COMITIA_HTTP_LOG=true`; an explicit flag overrides the
environment. Request logs contain only an allowlisted method, fixed route label
(`/v1/<operation>`, `/ui/` or `unknown`), status, duration in milliseconds and
server-generated request ID. They exclude request/response bodies, credentials,
headers, query strings and arbitrary paths. Logging is off by default.

The daemon closes connections whose headers take over 10 seconds or whose
request takes over 30, caps headers at 16 KiB, and closes idle connections
after 2 minutes.

SIGINT or SIGTERM stops `serve`, `mcp-http` and `mcp` cleanly: pending waits
return empty, the database is closed and the process exits 0.

Internal write failures produce secret-safe daemon diagnostics with operation,
error class, generated request ID, duration and build revision. Match the public
error's `request_id` (also `X-Request-ID` over HTTP) to the service log. Do not
retry a write blindly after a timeout: check the resource state or reuse the same
message dedupe key because the outcome can be uncertain.

### Credential expiry and rotation

Repair validates the saved expiry and authenticates the installed owner and
existing harness/viewer credentials against the database. Each recorded context
permission is checked against effective grants without writing a message,
including write-only credentials. Expired or revoked sessions or grants, broken
grant ancestry, and lost recorded permissions cause an actionable error;
a successful reinstall no longer leaves expired clients installed. Repair keeps
the database and credentials. Linux `INSTALLATION.txt` records the owner expiry.

Rotate before that expiry, with the installed daemon running:

```sh
# Linux
python3 scripts/install_linux_service.py rotate-credentials
# macOS
python3 scripts/install_macos_service.py --rotate-credentials
```

Rotation uses the existing REST mint endpoint to create an independent session
for the admin principal, rather than a child of the expiring owner. The new
owner issues its own root grants for the shared context and active contexts
readable by the source admin. Archived contexts and extra contexts that reject a
new grant are skipped and their IDs and reasons reported. Their historical read
access is **not transferred**: it remains with the old owner and existing readers
only while their sessions and grant ancestry remain valid. Old-owner expiry or
`--revoke-old` ends that access; archived data remains stored in the database.
The shared context must still accept grants, or rotation fails without publishing.
It then creates fresh sessions and grants for installed local
harnesses and the viewer, preserving their recorded local permissions. The new
owner lasts one year plus one hour; harness sessions last up to one year, with
root grants lasting longer than harness grants. This changes session IDs: restart
agent sessions and, on macOS, the viewer service after rotation. Child sessions
created for individual tasks retain their original ancestry and must be migrated
separately; credentials with non-local grants require manual rotation.

Every replacement is staged privately and checked against the installation's
database before publishing. An installation-wide `.install.lock` serializes
snapshot, minting, publication, and optional revocation, as well as install and
add-harness credential mutations and uninstall. A concurrent installer command fails clearly
with an installation-busy error; retry after the other command completes. The
lock is released automatically if its process exits. Original files are backed
up under a private `backups/credentials-*` directory; each file is replaced atomically. If publishing
is interrupted, credentials already replaced remain usable and rotation can be
rerun. Old sessions remain active until expiry by default. When other clients and
task sessions are ready to migrate, include `--revoke-old` on that rotation to
revoke the sessions replaced by that invocation and their descendants. This is
immediate; clients that still use those credentials lose access. Originals from
earlier rotations can be revoked separately through the admin API using the
session IDs in their backups. A failure during optional revocation occurs after
the new credentials have been published.

If the installed owner has already expired or been revoked, both commands accept
`--owner-credential /absolute/path/to/another-active-admin.json`. That credential
must belong to an active admin in the same installation. A live admin can also
replace expired harnesses through rotation. If **all admin sessions have expired**,
the API has no recovery path: preserve the database and restore a consistent
backup that still has a live admin credential. Reinstall cannot rebootstrap an
existing database; deleting it would discard the stored conversations and tasks.
Restoring a backup does not reset elapsed expiry: its admin must still be
unexpired at the time of recovery.
