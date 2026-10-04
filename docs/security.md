# Security model

## Scope

Comitia controls access to its own coordination records. Its security claims are limited to authenticating accepted credentials, evaluating stored authorization policy, and preserving state through its configured storage boundary. The current v1 model is a single-owner service with multiple workspaces and contexts. Separate tenant isolation and explicit principal workspace membership are target features, not current claims.

## Deployment boundary

Embedded mode assumes the host process and filesystem are trusted. Code in the process can bypass the application API, and any user able to read or modify the SQLite file can inspect or alter coordination state. Embedded authentication cannot create isolation from that host. `OpenSQLite` exclusively creates new on-disk databases with mode 0600; SQLite creates WAL/SHM sidecars with the same private permissions. Before opening, it rejects existing databases or sidecars that are symlinks, non-regular files, or readable/writable/executable by group or others (`ErrInvalid`). It preserves existing private files and leaves unsafe files unchanged; set their mode to 0600 before reopening. Parent directories still need filesystem protection. In-memory databases create no files.

Daemon and shared-server modes create a meaningful mediation boundary only when clients cannot bypass the daemon to access the database or credential material, including stored token hashes. Operators must protect file permissions, process credentials, network endpoints, backups, and logs. In a future multi-tenant deployment, tenant or workspace isolation must be enforced by server-side authorization on every operation, never by client-side filtering.

## Authorization invariants

The current contract requires these invariants:

1. A session acts only for its authenticated principal and only until it expires or is revoked.
2. A context grant is bounded by its parent authority, scope, context, and expiry.
3. Delegation can narrow authority but cannot widen it.
4. Grant validity depends on a live issuing session. Revoking an ancestor or issuer session invalidates authority derived from it.
5. Authorization is checked when an operation executes; a previously successful check is not a durable entitlement.
6. Context-scoped storage operations retain the applicable context boundary so an unrelated identifier cannot grant access.

Child contexts follow the same rules. Creating one requires `create` on the parent; administrative status alone is not enough. The creator's child grant names its parent grant as `ParentGrantID` and expires no later than it, so the existing grant-tree revocation and ancestry checks remove the creator's and its delegates' access to the child. A child is private by default: read on the parent reveals only its metadata (ID, name, creator session, parent task). Only a child created with `parent_readable` extends read, and only read, to sessions with read on the parent; inherited read is never a source for further delegation and does not make a session addressable as a message recipient in the child, which needs a direct read grant; it does allow the session's own acknowledgement cursor. Inherited read is not a grant, so such children appear in child listings but not in `ListContexts`. The creator's child grant is intentionally not a subset of its parent grant: `create` on the parent (with any other bits) yields read, write, claim, grant and create on the new child, because the creator owns it; its expiry and revocation still follow the parent grant. The child always belongs to the parent's workspace, and its task must be a task of the parent.

Context creation is bounded by `Config.MaxContextDepth` (default 3, top-level depth 0) and `Config.MaxChildContextsPerGrant` (default 20), exposed as `serve --max-context-depth` and `--max-child-contexts-per-grant`. Config zero values select defaults; configured limits must otherwise be positive. The count belongs to the exact parent grant selected at creation, not to a session or instance, and includes archived children. Creation and quota checks share a serialized transaction, so concurrent requests cannot exceed the quota. Existing contexts remain readable if a limit is later reduced.

Creation selects the longest-lived effective `create` grant with remaining capacity and refuses only when none has capacity. A holder of `grant` and `create` can multiply the count budget by delegating `create` to new grants; this quota bounds sprawl per grant, not per coordinator or delegation tree. The limit flags belong only to `serve` and are not persisted in the database. Embedded CLI commands and embedded MCP use the defaults even when sharing a database with a differently configured server; remote clients use the server's configured limits. Embedded library applications can set the Config fields directly.

`ArchiveContext` / `POST /v1/contexts/archive` / `context-archive --context-id` requires an effective `grant` permission on the context or administrative principal status; context `admin` permission alone does not suffice. Archiving is idempotent and permanent. Every context-content write is rejected after archive: messages (including retries), references, task creation/claim/renewal/completion, new grants including session delegation, and child creation. Administrators cannot bypass this write guard. Revoking grants/sessions remains available. Replay, task reads, context/child listing, and acknowledgement cursors remain subject to normal read authorization; archiving does not grant visibility or revoke existing readers. Explicitly archiving a parent does not cascade archive state to children.

The context lifecycle side tables record explicit archive timestamps and the exact creator child grant plus its creating parent grant. A child's effective `Archived` state is explicit archival or the original creator grant no longer being effective, including expiry/revocation anywhere in its grant/owner/issuer chain. `ArchivedAt` is the earliest explicit archive or access-ending timestamp. Evaluation happens on each metadata read and write check, without a background job. A new grant to the creator cannot keep the context writable after its original grant ends. Remaining independent grant holders and eligible inherited parent readers can still read; delegated grants beneath the ended creator chain stop working under the usual ancestry rules. Existing child contexts are backfilled from their original creator grants when the SQLite store opens.

The target multi-tenant model additionally requires explicit principal membership in a workspace before accessing that workspace's resources.

Non-admin sessions may revoke only grants whose `IssuerSessionID` is their own
session ID, and sessions whose `ParentID` is their own session ID. Holding a
grant, owning an ancestor grant, or sharing a principal with the issuer does not
authorize revocation. Administrative principals may revoke any grant or session.
Revocation cascades through the existing grant/session trees. CLI, HTTP and MCP
all call these service methods; MCP does not add administrative authority.

### Inviting sessions

A non-admin session holding `grant` can invite an unrelated session to a context in the same workspace. Sessions and principals have no explicit workspace membership field: the target qualifies only when it holds its own currently valid grant on any context in that workspace. Grants held by ancestor sessions do not establish membership, since an admin or coordinator may hold authority in workspaces its children do not belong to. Expired or revoked grants, including invalid grant ancestry or issuers, do not qualify. Another session belonging to the same principal does not establish membership. A session may qualify for multiple workspaces; holding authority only in another workspace does not qualify. Direct-child grants and administrative grants retain their existing rules, including the ability to establish initial access.

Each invitation must be covered by one live issuer grant containing `grant` and every requested permission. Permissions from multiple grants are not combined. Expiry cannot exceed that issuer grant, the issuing session, or the target session. The invitation records `IssuerSessionID` and uses the issuer grant as `ParentGrantID`, so revoking that grant cascades to invitees and their delegated grants. Invitations add access immediately without target acceptance; workspace eligibility does not itself grant access to the destination context. Ineligible workspace targets and insufficient issuer authority return `ErrForbidden`; zero permissions or invalid/session-unbounded expiry return `ErrInvalid`.

Use `comitia grant --session-id SESSION --context-id CONTEXT --permissions read --ttl 30m` with an authenticated credential. Embedded and remote CLI modes use the same `GrantContext` API; remote mode calls `grants/create`. The CLI returns the created grant, including its issuer and parent grant IDs.

Initial bootstrap is a one-time local or offline operation. Network adapters must not expose it; doing so would turn possession of a bootstrap secret into a remote administrative enrollment path.

## Credential proof

Successful authentication means only that the request presented valid proof for a configured credential. The initial credential mechanism uses static, opaque scoped tokens; it does not implement OIDC. Authentication does not establish real-world identity, intent, software integrity, or the accuracy of claims in messages and task results. Deployments needing stronger identity assurance must bind credentials to an external identity process and document that process separately.

Secrets must not be stored in resource references, messages intended for broad replay, command arguments visible to other local users, or logs. Error responses should contain enough detail to diagnose a request without revealing credentials or cross-workspace resource existence. Task ID operations authenticate before lookup and return `ErrForbidden` for both missing tasks and tasks outside the required context authority, including for administrators without that authority. Non-admin grant/session revocation returns `ErrForbidden` for both missing IDs and IDs the caller cannot revoke. Grant creation verifies non-admin context grant authority before consulting context or target IDs and returns `ErrForbidden` for missing or ineligible targets. Administrative grant/session operations retain `ErrNotFound` diagnostics after authentication. Invalid or absent credentials return `ErrUnauthenticated` independently of those resource IDs.

MCP `delegate_session` never returns a token. The server process stores the CLI
delegation document plus `server_url` in a new `<session_id>.json` file opened
with `O_EXCL` and mode 0600; its configured directory is created with mode 0700.
Existing directories must have mode 0700, be owned by the effective user and
must not be symlinks; the directory is opened once and the opened handle is
checked against the path, so a symlink swapped in after the check is refused.
Session IDs
are validated before deriving filenames. Failed writes remove partial files
and attempt session revocation using a short cleanup context independent of
request cancellation. If the daemon is unavailable, cleanup may fail; the
session still has its bounded expiry, and the tool error and the process log
name the session ID (not a secret) so an operator can revoke it. Errors are
otherwise sanitized and tokens are not logged. Files persist after
revocation/expiry; operators manage their retention with the installed Linux wrapper
`comitia-client sessions --prune` ([scope and status checks](local-service.md#spawn-flow-a-delegated-session-per-task-owner)).
This removes expired/revoked delegated files in the configured sessions
directory; it is not a bare Go CLI command or an MCP tool.

`mcp-http` hides `delegate_session` from `tools/list` and refuses calls to it
unless started with `--allow-delegation` (or `COMITIA_MCP_HTTP_ALLOW_DELEGATION`):
a remote caller cannot read a server-local credential file, so delegation over
HTTP only makes sense when a launcher on the server host consumes the files.
Stdio `mcp` always offers it.

`COMITIA_MCP_HTTP_ALLOW_DELEGATION` and `COMITIA_MCP_HTTP_LOG` default to
false when unset or empty. Accepted true values are `1`, `t`, `T`, `true`,
`TRUE`, `True`; accepted false values are `0`, `f`, `F`, `false`, `FALSE`,
`False`. Other values fail startup. Explicit `--allow-delegation` and
`--log-requests` flags take precedence over their respective environment
variables, even invalid ones; use `=false` to disable either feature.

Optional `mcp-http --log-requests` logging records only an allowlisted HTTP
verb (unknown verbs become `other`), the constant `/mcp` route, status and
duration. Request paths, query strings, headers (including protocol and MCP
method headers), remote addresses and bodies are excluded because they can
contain credentials or private coordination content.

Grant expiries are computed on the MCP host clock, the session expiry on the
daemon clock. With a remote daemon whose clock is behind the MCP host, a grant
TTL equal to the session TTL can exceed the session and is rejected with
`ErrInvalid`.

The result exposes only the session ID, principal ID, expiry, grant IDs and
absolute credential path. The path is a handoff to a trusted launcher on the
same machine, not a request to read the secret into a model. Remote MCP callers
receive a server-local path. Files and the MCP process share the trust of their
OS account: an operator or process with access to that account can read them.
Configure `--sessions-dir` or `COMITIA_SESSIONS_DIR` on the MCP process, rather
than accepting a path from tool arguments. The daemon URL is also process
configuration (`--server-url` / `COMITIA_SERVER_URL`, or
`mcp --credential-server-url` for embedded SQLite), so credential files do not
silently fall back to an unrelated installation. CLI/REST delegation still
returns credentials to trusted callers and must not be used as a model tool.

## Replay and concurrency

Message delivery is at least repeatable: reconnects and retries can expose the same message more than once. Deduplication keys prevent duplicate acceptance of a logical write, while consumer acknowledgements track replay progress. Neither mechanism makes external side effects exactly once; consumers must make those effects idempotent.

Task leases require fencing. Every owner-only mutation must verify the current lease identity and validity in the same transaction as the mutation. Expired owners are untrusted for state changes even if they later reconnect.

## Operational considerations

- Use restrictive permissions for database, configuration, sockets, backups, and secret files.
- Terminate and authenticate network connections according to the deployment's threat model.
- Rotate credentials and revoke affected sessions and descendant grants after suspected compromise.
- Avoid logging bearer credentials and sensitive message bodies.
- Use the [private online snapshot and isolated restore drill](backup-restore.md), including authorization ancestry, instance cursors and lease state. Copying live database and WAL files is not assumed to be safe. Restores can resurrect old claims and revoked credentials; fence workers and reconcile external effects before resumption.
- Keep SQLite and the Comitia binary updated for security fixes.

Owner access is time bounded. Before the current owner session expires, use its administrative authority to mint a replacement owner session and store the new credential securely. A live administrative credential can restore access by granting its replacement session the required contexts. If every administrative credential expires or is lost, the current API has no supported account-recovery or offline bypass; the existing coordination data remains inaccessible through Comitia.

## Non-goals

Comitia does not attest agent behavior, validate the safety of external resources, isolate code executed in the same embedded process, or provide end-to-end secrecy from an operator with database access. It also does not make external systems consistent with its state.
