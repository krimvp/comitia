# Instance presence and resume metadata

`list_instances(context_id)` lists recorded activity for self-reported processes
in one readable context. Each row has `id`, optional `label`, `session_id`,
`context_id`, `last_seen` (UTC timestamp), `active`, `seen_seconds_ago`, `recent`,
and optional `resume` JSON object. `active` is false when the credential session
is revoked or expired; it does not say whether the process is running or still has a grant on the context.
`seen_seconds_ago` is a server-computed whole-second age, clamped to zero for
future timestamps. `recent` means age < **90 seconds**, independent of `active`.
Older servers omit these fields.
Instance IDs and labels follow the [message instance rules](message-protocol.md).
Rows are keyed by **(authenticated session, context, instance ID)**, so the same
instance ID in different sessions or contexts has independent metadata.

Presence is self-reported routing metadata, never authorization or identity
proof. A recent timestamp records a successful call, not a running-process
check. Rows of a revoked or expired session stay listed for **24 hours**, then are
hidden and pruned by later presence writes;
`list_participants` lists current readable credential sessions. Each session
keeps at most **64** instances per context: a new one evicts that session's
least recently seen row, never another session's.
Comitia never launches or resumes processes. See the
[external supervisor registry and steering recipe](durable-coordination.md#supervised-process-registry-and-steering)
for launcher-owned process identity, bounded checkpoints, separate watcher cursors
and semantic control receipts. Never reclaim work solely on stale activity.

Successful send, replay, wait, cursor and ack calls naming an instance update its last seen.
`claim_task`, `renew_task`, and `complete_task` (including claim/renew retries)
touch the claim's instance. Renew/complete preserve labels and resume. Failed
task calls record no activity. Message activity includes
send retries and empty reads. Replay and wait accept
an optional **ID string** `agent_instance`, like cursor and ack; CLI `replay`,
`wait`, and `watch` accept `--agent-instance ID`, and `watch` records activity
through its waits and acks. When omitted, MCP, the CLI and the HTTP client use
`COMITIA_AGENT_INSTANCE`; an explicit empty ID records no activity and creates
no anonymous row. Heartbeats do not change replay filtering or acknowledgement cursors.

Heartbeats are normally throttled to at most once every **30 seconds per (session,
context, instance)**. A changed nonempty label on send or claim also makes a
heartbeat due, so the label refreshes immediately within that interval. Send,
ack, claim, renew and complete check the stored timestamp and perform any due
upsert inside their existing write transaction. A SQLite savepoint rolls back
failed heartbeats without undoing the message, ack or task mutation. Replay,
wait and cursor check freshness in their read-only View; a missing or stale row
gets a separate small Update that rechecks it. These
optional read-triggered writes have a **25 ms budget**, further limited by the
caller or wait deadline, and are skipped or rolled back on contention or timeout.

Last seen normally lags activity by up to 30 seconds and
may lag longer when optional writes fail or time out. ID-only activity preserves
the previous label and resume data. Use set_instance_resume to update
metadata immediately.

A process can set or replace its resume object through MCP (an omitted
`agent_instance` defaults to `COMITIA_AGENT_INSTANCE` /
`COMITIA_AGENT_INSTANCE_LABEL`, as for `send_message`):

```json
{
  "context_id": "CONTEXT_ID",
  "agent_instance": {"id": "worker", "label": "Issue worker"},
  "resume": {"harness": "codex", "session_id": "harness-session", "cwd": "/tmp/work", "pid": 123}
}
```

Call `set_instance_resume` with these arguments. Resume is an arbitrary JSON
object of at most **4096 bytes**; arrays, null, scalars, malformed JSON, and
oversize objects return `ErrInvalid`. Numbers keep full precision. `{}` clears the object's contents. The
write also records last seen, requires read permission and is rejected in
archived contexts (read heartbeats there continue). It
only writes the authenticated session's row. Like acknowledgement cursors,
presence is **read-level bookkeeping**: even a read-only or invited observer may
create and overwrite its own instance rows and labels. It cannot overwrite a
row belonging to a different authenticated session, and it does not gain message
write permission. Explicit resume writes are unthrottled and return
storage errors, unlike opportunistic heartbeats. Do not put credentials or
secrets in resume metadata: all authorized readers of that context can read it.

CLI:

```sh
comitia instance-resume --context-id CONTEXT_ID --agent-instance worker \
  --agent-instance-label 'Issue worker' --resume-file resume.json
comitia instances --context-id CONTEXT_ID --page --limit 100
comitia replay --context-id CONTEXT_ID --agent-instance worker --after 0 --limit 100
```

HTTP uses authenticated POST requests with `Content-Type: application/json`:

| Route | Request |
| --- | --- |
| `/v1/instances/list` | `{"context_id":"CONTEXT_ID","page":true,"limit":100}` |
| `/v1/instances/resume` | `{"context_id":"CONTEXT_ID","instance":{"id":"worker"},"resume":{"pid":123}}` |
| `/v1/messages/replay` | `{"context_id":"CONTEXT_ID","agent_instance":"worker","after":0,"limit":100}` |
| `/v1/messages/wait` | `{"context_id":"CONTEXT_ID","agent_instance":"worker","after":0,"timeout_seconds":30}` |

HTTP uses `instance` for the resume instance object because its existing
`agent_instance` request field holds a cursor/replay ID string. Lists return at
most 100 rows unless `limit` (1-1000) is given; `page` or `after_id` (CLI
`--page`, `--after-id`) returns `instances`, `next_after` and `has_more` as for
participants (HTTP: `instance_page`; unpaged: `instances`); the CLI and MCP
return arrays directly. No administrative
bypass permits listing an unreadable context.

Go service and HTTP client methods:

```go
ListInstances(ctx context.Context, token, contextID string) ([]comitia.InstancePresence, error)
ListInstancesPage(ctx context.Context, token, contextID, after string, limit int) (comitia.InstancePage, error)
SetInstanceResume(ctx context.Context, token, contextID string, instance *comitia.AgentInstance, resume json.RawMessage) error
ReplayMessages(ctx context.Context, token, contextID string, after int64, limit int, filters ...comitia.MessageFilters) ([]comitia.Message, error)
WaitMessages(ctx context.Context, token, contextID string, after int64, timeout time.Duration, filters ...comitia.MessageFilters) ([]comitia.Message, error)
```

Reads name the caller's instance with `MessageFilters.AgentInstance` (a `*string`);
it never narrows the results. The service records no activity for nil or empty;
the HTTP client substitutes its configured default for nil. Custom API implementations must add
`PresenceAPI` (paging via `InstancePageAPI` is optional); custom stores implement
`Instances(contextID, now)`, `InstanceLastSeen(sessionID, contextID, instanceID)`,
`PutInstancePresence` for explicit resume writes, and `TouchInstancePresence` for
best-effort heartbeats. TouchInstancePresence must isolate a failed heartbeat
from its caller's transaction. Stores may implement `InstancesAfter` for paging
and `InstanceLabel(sessionID, contextID, instanceID)` for label lookups;
otherwise the service uses `Instances`. The cap and retention are SQLite behaviour. SQLite adds
`instance_presence` without altering existing tables; earlier message history
is not backfilled as activity.
The inspector shows instances in its sidebar and refreshes them alongside messages.
