# Backup and restore reconciliation

Run this drill before depending on a backup. SQLite integrity alone does not
prove that credentials still authenticate, or that a restored worker may safely
resume. Comitia coordinates workers; it does not stop their processes or undo
external effects.

## Take a private online snapshot

From a trusted checkout, with Python 3.11+ and a local Linux/macOS installation:

```sh
# The parent of each output directory must already exist; outputs must be new.
install -d -m 700 "$HOME/comitia-recovery"
python3 scripts/comitia_backup.py snapshot \
  --root "$HOME/.local/share/comitia" \
  --output "$HOME/comitia-recovery/snapshot-1" \
  --config "$HOME/.codex/config.toml" \
  --config "$HOME/.claude.json" \
  > "$HOME/comitia-recovery/snapshot-report.json"
chmod 600 "$HOME/comitia-recovery/snapshot-report.json"
```

Include only config paths that exist. Repeat `--config` for other private
harness, proxy or service configuration. The output directory is mode 0700;
files are 0600, and its saved binary is 0700, even with a permissive umask.
Protect reports and off-machine copies too; use encrypted storage and private
access. The bundle contains bearer credentials and coordination data. Restore
only trusted bundles: SHA-256 checks detect damage, not malicious replacement.
The saved binary must run on the restore host's OS/architecture; the SQLite
snapshot itself is portable.

The script takes the existing installation flock, excluding installer repair,
credential rotation and harness registration. Normal daemon writes can continue:
Python's SQLite online backup API includes committed WAL content in one
consistent database. A copy of a live main file can omit those commits; copying
DB/WAL/SHM separately does not form a consistent snapshot. The lock does not
exclude manual config editors or external API administrators. Avoid those changes
while taking a snapshot, and retain a separate log of later revocations,
rotations, worker receipts and external effects.

The bundle saves `data/comitia.db`, all installed credential JSON files,
`local-context.json`, and any present `harnesses.json`, `service.json` and
`INSTALLATION.txt`. Extra config files are private numbered copies under
`config/`, mapped to their original paths in `manifest.json`. They are never
written back to the original paths by the drill. The installed `bin/comitia`,
its SHA-256 and reported build identity establish the rollback floor described
below. The manifest is written last; an interrupted bundle without it is
incomplete and must be discarded. Ordinary errors remove the partial output.

A matching, still-valid administrator is mandatory. If the installed owner is
unusable, supply `--owner-credential /private/matching-admin.json`, as with
credential rotation. The supplied credential must authenticate against this
database and belong to an admin principal; a readable delegate is insufficient.
The script verifies authoritative expiry/revocation and checks saved delegate
grants through #51's installer validation. Unusable delegates are listed in
`checks.unusable_credentials` for replacement; they do not prevent backing up
an otherwise recoverable installation. Restoring hashes cannot reconstruct
lost bearer tokens, and restoring an old snapshot cannot extend credential
expiry. Rotate credentials **before** the last administrator expires; if all
administrators have expired, this drill rejects the bundle as a recovery source.

## Restore and check in isolation

```sh
python3 scripts/comitia_backup.py restore \
  --snapshot "$HOME/comitia-recovery/snapshot-1" \
  --output "$HOME/comitia-recovery/drill-owner" \
  > "$HOME/comitia-recovery/drill-owner-report.json"
chmod 600 "$HOME/comitia-recovery/drill-owner-report.json"

# Check a real saved harness's instance cursor in a second isolated copy.
python3 scripts/comitia_backup.py restore \
  --snapshot "$HOME/comitia-recovery/snapshot-1" \
  --output "$HOME/comitia-recovery/drill-codex" \
  --credential codex --agent-instance codex-project-worker \
  > "$HOME/comitia-recovery/drill-codex-report.json"
chmod 600 "$HOME/comitia-recovery/drill-codex-report.json"
```

Use the actual saved credential name and instance ID. Without
`--agent-instance`, the drill reads the legacy session cursor. No listener or
worker is launched. The output must be a new directory, separate from both the
source installation and snapshot. The script never replaces an existing
installation, modifies service units or registers harnesses. It clears inherited
`COMITIA_*` routing/credentials through the shared embedded runner, so a daemon
URL in saved config cannot send the drill to production.

Every run verifies bundle hashes before executing its saved binary, then checks
SQLite `integrity_check` and `foreign_key_check`, admin authentication, and
credential/grant ancestry at the current wall clock. It performs authenticated
replay of the first message, reads the selected session/instance cursor, and
gets every task in the installed shared context, including lease version,
expiry and owner instance. `restore-report.json` is private and always says
`reconciliation_required: true`. Read it and compare the sample message,
cursor and tasks with saved receipts. Repeat the check for every worker that
will resume. The automatic check covers the installation's shared context;
use the saved binary against the isolated DB to inspect other contexts, grants,
archival and application-specific invariants before recovery.

## Reconcile before resuming

1. **quiesce and fence workers.** Stop the daemon, MCP adapters and every worker
   able to write to it or external systems, including remote workers. Verify
   processes have stopped and invalidate their external credentials or use a
   new run generation if a partitioned worker cannot be confirmed stopped.
   Comitia presence and a SQLite lease are not process supervision or an
   external-system fence. Keep the service stopped throughout publication and
   reconciliation; pause scheduled service restarts too.
2. Compare snapshot time with the retained revocation/rotation and task receipt
   log. A restore rolls back acknowledgements, dedupe records, revocations,
   completions and lease token/version changes. An old claim receipt rejected
   by the current DB can become valid against the snapshot, and a revoked token
   can authenticate again. Reapply post-snapshot session and grant revocations
   with the restored admin (`revoke --session-id S`,
   `grant-revoke --grant-id G`). If the history is incomplete, revoke every
   restored worker session and mint fresh sessions/grants before reconnecting.
3. Inspect every restored leased task with `task-get`. After workers are fenced,
   release each claim using the restored context admin and its **current
   restored** version: `task-release --task-id T --lease-version N` (no claim
   token required for a context admin). Verify it is pending, or record a
   reconciled completion with a fresh claim. Never replay old receipts to
   perform work. Do not assume a numerically higher restored version fences a
   pre-restore external effect; use a new external run identity.
4. Compare instance acknowledgements and dedupe keys with consumers' durable
   receipts. Replay can repeat accepted messages and writes; consumers need
   idempotent processing and dedupe keys scoped to the new run. Decide explicitly
   which messages/effects have already been applied. Do not bulk-ack unread
   restored messages to suppress duplicates.
5. Query each external authoritative system for uncertain effects: exact Git
   commit/branch heads, PR IDs and merge state, job IDs, deployed artifacts and
   payment/job receipts as applicable. An absent Comitia receipt is not proof
   that an action did not happen. Record confirmed outcomes; retry only after
   verifying idempotency or reconciling the external state.
6. Publish the reconciled DB and matching private credentials/config while all
   processes remain stopped. Preserve the current installation as a separate
   recovery copy first. Never overwrite only `comitia.db` beside existing WAL
   sidecars: replace the stopped data directory as a unit, retaining the old
   directory separately. Keep the reconciled binary with its matching database;
   restore deployment paths/ports deliberately rather than blindly copying
   config onto another host. Starting fresh workers with explicit new instance
   IDs avoids treating stale presence as live ownership. Check authentication
   and lease state again before enabling work. Use the installer
   `rotate-credentials --revoke-old` (macOS: `--rotate-credentials --revoke-old`)
   with a running but worker-isolated daemon to mint independent replacements
   and grants; restart harnesses to load them. Reapply revocations first so this
   maintenance listener never exposes resurrected credentials to workers.

Actual publication is an operator step, separate from the repeatable isolated
drill. Service stop/start and filesystem replacement must match the deployment;
see [local service management](local-service.md). The repository regression
suite demonstrates WAL preservation, auth/cursor/task restoration, and the
renewed-receipt/revocation/ack rollback hazard entirely in temporary directories:

```sh
GOTOOLCHAIN=go1.24.13 GOFLAGS=-buildvcs=false \
  python3 -m unittest scripts/test_comitia_backup.py
```

## Supported rollback floor

Use the exact saved binary for the initial drill. Its checksum and
`manifest.json` build identity are the snapshot's supported rollback floor.
A binary older than the snapshot's policy is unsupported even if it opens the
schema: older versions can ignore later authorization or archive restrictions.
This tool deliberately offers no alternate-binary flag. An exact-version
restore establishes snapshot behavior, not permission to reintroduce a known
security defect. Apply required security fixes and repeat the isolated drill
with the current supported release before production use. Upgrading a clone
is a separate migration/policy validation; never test a downgrade on the only
recovery copy. Unknown build identity requires operator provenance verification;
the exact binary hash still identifies the bytes used by the drill.
