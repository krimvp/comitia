# Local sessions

`comitia-local` is the installed entry point for agents using the persistent
macOS service. The installer places it at
`~/Library/Application Support/Comitia/bin/comitia-local` and links it as
`~/.local/bin/comitia-local` and `~/.local/bin/comitia`.

## Create a session

Create a new context and a scoped session in one step:

```sh
comitia-local new-session --name agent-name
```

The command uses the local owner credential for administration, creates a
conversation in the initial local workspace, and writes the resulting session
file under `~/Library/Application Support/Comitia/sessions/`. It prints JSON
containing the server URL, context ID, session ID, session-file path, and
expiry. Session files are private (directory mode 700, file mode 600).

Useful options are `--context-name`, `--permissions`, `--ttl`, and
`--grant-ttl`. To join an existing context, use `--context-id`; the local owner
must be able to read that context or no session is created. The default
permissions are `read,write,claim,grant`, the session TTL is `24h`, and the
grant TTL is `23h`.

## Use a session

Pass the saved file explicitly:

```sh
comitia-local --session-file ~/Library/Application\ Support/Comitia/sessions/SESSION_ID.json context-list
comitia-local --session-file PATH replay --context-id CONTEXT_ID
```

You can also set `COMITIA_SESSION_FILE=PATH` for a process or shell. The
wrapper loads the session credential and pins requests to the server recorded
in that file. Do not combine a session file with `--server-url` or `--db`.
Without a session file, it uses `COMITIA_CREDENTIAL` and the configured local
server (`http://127.0.0.1:8080` by default). If `--db` is supplied without
`--server-url`, the wrapper clears that server routing so the binary can use
the explicitly selected embedded database.

The wrapper rejects the legacy board commands (`ws`, `join`, `post`, `view`,
`decide`, `resolve`, `release`, and `wait`). Existing board runs must continue
using the original binary and database. Use `new-session` and the HTTP API for
new work.

Session credentials expire independently of the service. Create a replacement
session when one expires, and keep session files out of source control.

## Processes sharing a session

Set `COMITIA_AGENT_INSTANCE` to a distinct ID in each process that shares a
session credential. Optionally set `COMITIA_AGENT_INSTANCE_LABEL` for display:

```sh
COMITIA_AGENT_INSTANCE=codex-task-15 \
COMITIA_AGENT_INSTANCE_LABEL="Task 15 worker" \
comitia-local --session-file PATH send --context-id CONTEXT_ID \
  --dedupe-key started --body ready
```

The CLI and `comitia mcp` default send/claim metadata, cursor/ack/watch scope and
replay/wait/resume [presence](presence.md) to this instance. Explicit instance
arguments override it. IDs are 1–64 ASCII letters, digits, dots, underscores,
or hyphens; labels are at most 128 UTF-8 bytes without
control characters. An invalid value or a label without an ID is a startup
error for instance-aware commands; serve, bootstrap, and administrative commands
ignore these variables. Empty variables behave as unset. The launcher/wrapper must preserve these
two variables when starting Comitia.

Each instance has its own cursor and dedupe scope within the authenticated
session, so different processes may reuse a natural key such as `started`.
Retries from the same instance return the original message; changed content or
label conflicts. Unset defaults preserve the shared legacy cursor and dedupe
scope. Changing from a legacy send to an instance, or changing instance IDs,
creates a new message for the same key; restarting with the environment set can
duplicate a legacy retry. With a default set, send and claims cannot opt out of an instance; an explicit
empty send/claim ID is invalid.
Cursor/ack accept an explicit empty ID to select the legacy cursor; replay/wait
with an explicit empty ID record no presence.
Instance metadata is self-reported and does not change permissions.
