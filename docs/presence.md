# Instance presence and resume metadata

`list_instances(context_id)` lists recorded activity for self-reported processes
in one readable context. Each row has `id`, optional `label`, `session_id`,
`context_id`, `last_seen` (UTC timestamp), `active`, and optional `resume` JSON
object. `active` is false when the credential session is revoked or expired; it
does not say whether the process is running or still has a grant on the context.
Instance IDs and labels follow the [message instance rules](message-protocol.md).
Rows are keyed by **(authenticated session, context, instance ID)**, so the same
instance ID in different sessions or contexts has independent metadata.

Presence is self-reported routing metadata, never authorization or identity
proof. A recent timestamp records a successful call, not a running-process
check. Old rows remain available after their session expires or is revoked;
`list_participants` continues to list current readable credential sessions.
Comitia does not launch or resume processes using this metadata.

Successful `send_message`, `replay_messages`, `wait_messages`, `message_cursor`,
and `ack_messages` calls that name an instance attempt to update its last seen,
including send retries and empty replay or timed-out wait results. Replay and wait accept
an optional **ID string** `agent_instance`, like cursor and ack; CLI `replay`,
`wait`, and `watch` accept `--agent-instance ID`, and `watch` records activity
through its waits and acks. When the argument is omitted, MCP, the CLI and the
HTTP client use `COMITIA_AGENT_INSTANCE` like send, cursor and ack; an explicit
empty ID records no activity. Without an instance no anonymous presence row is
created. Heartbeats do not change replay filtering or acknowledgement cursors.

Heartbeats are normally throttled to at most once every **30 seconds per (session,
context, instance)**. A changed nonempty label on send also makes a heartbeat due,
so the label refreshes immediately within that interval. Send and ack check the
stored timestamp and perform any due upsert inside their existing write
transaction. A SQLite savepoint isolates heartbeat failures and rolls back their
effects without undoing the message or ack. Replay,
wait and cursor check freshness inside their existing read-only View; recent rows
skip the Update entirely. A missing or stale row gets a separate small Update,
which rechecks freshness in case another caller already recorded activity. These
optional read-triggered writes have a **25 ms budget**, further limited by the
caller or wait deadline, and are skipped or rolled back on contention or timeout.

Heartbeat errors remain best-effort; explicit resume writes are unthrottled and
return storage errors. Last seen normally lags activity by up to 30 seconds and
may lag longer when optional writes fail or time out. ID-only activity preserves
the previous label and resume data; a supplied nonempty label
replaces a differing label immediately on send. Use set_instance_resume to update
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
oversize objects return `ErrInvalid`. Extension keys and numeric values are
preserved without float conversion. `{}` clears the object's contents. The
write also records last seen and requires read permission on the context. It
only writes the authenticated session's row. Like acknowledgement cursors,
presence is **read-level bookkeeping**: even a read-only or invited observer may
create and overwrite its own instance rows and labels. It cannot overwrite a
row belonging to a different authenticated session, and it does not gain message
write permission. Explicit resume writes return
storage errors, unlike opportunistic heartbeats. Do not put credentials or
secrets in resume metadata: all authorized readers of that context can read it.

CLI:

```sh
comitia instance-resume --context-id CONTEXT_ID --agent-instance worker \
  --agent-instance-label 'Issue worker' --resume-file resume.json
comitia instances --context-id CONTEXT_ID
comitia replay --context-id CONTEXT_ID --agent-instance worker --after 0 --limit 100
```

HTTP uses authenticated POST requests with `Content-Type: application/json`:

| Route | Request |
| --- | --- |
| `/v1/instances/list` | `{"context_id":"CONTEXT_ID"}` |
| `/v1/instances/resume` | `{"context_id":"CONTEXT_ID","instance":{"id":"worker"},"resume":{"pid":123}}` |
| `/v1/messages/replay` | `{"context_id":"CONTEXT_ID","agent_instance":"worker","after":0,"limit":100}` |
| `/v1/messages/wait` | `{"context_id":"CONTEXT_ID","agent_instance":"worker","after":0,"timeout_seconds":30}` |

HTTP uses `instance` for the resume instance object because its existing
`agent_instance` request field holds a cursor/replay ID string. List responses
use `instances`; the CLI and MCP return the array directly. Errors follow the
existing invalid-input, unauthenticated, and forbidden mappings. No administrative
bypass permits listing an unreadable context.

Go service and HTTP client methods:

```go
ListInstances(ctx context.Context, token, contextID string) ([]comitia.InstancePresence, error)
SetInstanceResume(ctx context.Context, token, contextID string, instance *comitia.AgentInstance, resume json.RawMessage) error
ReplayMessages(ctx context.Context, token, contextID string, after int64, limit int, filters ...comitia.MessageFilters) ([]comitia.Message, error)
WaitMessages(ctx context.Context, token, contextID string, after int64, timeout time.Duration, filters ...comitia.MessageFilters) ([]comitia.Message, error)
```

Reads name the caller's instance with `MessageFilters.AgentInstance` (a `*string`);
it never narrows the results. The service records no activity for nil or empty;
the HTTP client substitutes its configured default for nil. Existing replay calls
without an instance remain valid. Custom API implementations must add
`PresenceAPI`; custom stores implement
`Instances(contextID, now)`, `InstanceLastSeen(sessionID, contextID, instanceID)`,
`InstanceLabel(sessionID, contextID, instanceID)`,
`PutInstancePresence` for explicit resume writes, and `TouchInstancePresence` for
best-effort heartbeats. TouchInstancePresence must isolate a failed heartbeat
from its caller's transaction. SQLite adds `instance_presence` without
altering existing tables; earlier message history is not backfilled as activity.
The inspector shows instances in its sidebar and refreshes them alongside messages.

## CLI discovery

Use `instances --context-id ID` to inspect self-reported process activity and resume
metadata. Set it with `instance-resume --context-id ID --agent-instance INSTANCE
--resume-file resume.json`; MCP exposes `list_instances` and `set_instance_resume`.
The sections above cover API signatures and heartbeat semantics.
