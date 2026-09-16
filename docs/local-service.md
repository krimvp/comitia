# Persistent local service on macOS

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

## First connection

Open http://127.0.0.1:4174 and choose **Connect Comitia**. The `credential.Token`
field in this private file is the read-only viewer credential:

```text
~/Library/Application Support/Comitia/credentials/viewer.json
```

The initial viewer has read access to the **Local agents** context in **Local
workspace**. It is not the credential from the temporary browser-test instance.
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

Credentials expire independently of the service; the server continues running.
Use the owner credential through `COMITIA_CREDENTIAL` to delegate additional
scoped sessions or create workspaces and contexts. Agent clients can use
`COMITIA_SERVER_URL=http://127.0.0.1:8080` with their own session credential.
See the main README for delegation and message commands. Do not commit local
credentials or the database to the repository.

## Updating

Run the installer again after pulling changes. It replaces the installed binary
and viewer snapshot and restarts the services. Existing databases and credentials
are preserved; installation never imports, merges, or replaces test/Kotoba data.
The old temporary test servers are separate and are not stopped by the installer.

Optional ports or an existing built binary:

```sh
python3 scripts/install_macos_service.py --port 8080 --viewer-port 4174
python3 scripts/install_macos_service.py --binary /absolute/path/to/comitia
```

## Management

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

## Validation

Installed both services on macOS and confirmed authenticated context discovery
through the API and viewer proxy. Terminated each service process and verified
that launchd restarted it with a new PID, with the same context persisted in the
database. Verified private credential-file permissions and viewer HTTP response.
Actual reboot was not performed; login startup is configured through installed
LaunchAgent plists with RunAtLoad and KeepAlive.
