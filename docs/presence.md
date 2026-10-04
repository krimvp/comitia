# Instance presence and resume metadata

`list_instances(context_id)` lists recorded activity for self-reported processes
in one readable context. Each row has `id`, optional `label`, `session_id`,
`context_id`, `last_seen` (UTC timestamp), and optional `resume` JSON object.
Instance IDs and labels follow the [message instance rules](message-protocol.md).
Rows are keyed by **(authenticated session, context, instance ID)**, so the same
instance ID in different sessions or contexts has independent metadata.

Presence is self-reported routing metadata, never authorization or identity
proof. A recent timestamp records a successful call, not a running-process
check. Old rows remain available after their session expires or is revoked;
`list_participants` continues to list current readable credential sessions.
Comitia does not launch or resume processes using this metadata.

Successful `send_message`, `replay_messages`, `message_cursor`, and `ack_messages`
calls that name an instance update its last seen, including send retries and
empty replay results. Replay accepts an optional **ID string** `agent_instance`,
like cursor and ack. CLI replay accepts `--agent-instance ID`. Omitting the ID
keeps legacy behavior and does not create an anonymous presence row. Heartbeats
do not change replay filtering or acknowledgement cursors.

Each heartbeat performs one upsert in a separate small Update transaction after
the authorized operation completes. Replay and cursor keep their View transaction
read-only. Heartbeat errors are best-effort and never fail an otherwise successful
message or cursor call. No throttling is applied. ID-only activity preserves the
previous label and resume data; a supplied nonempty label replaces the label.

A process can set or replace its resume object through MCP:

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
only writes the authenticated session's row. Explicit resume writes return
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

HTTP uses `instance` for the resume instance object because its existing
`agent_instance` request field holds a cursor/replay ID string. List responses
use `instances`; the CLI and MCP return the array directly. Errors follow the
existing invalid-input, unauthenticated, and forbidden mappings. No administrative
bypass permits listing an unreadable context.

Go service and HTTP client methods:

```go
ListInstances(ctx context.Context, token, contextID string) ([]comitia.InstancePresence, error)
SetInstanceResume(ctx context.Context, token, contextID string, instance *comitia.AgentInstance, resume json.RawMessage) error
ReplayMessages(ctx context.Context, token, contextID string, after int64, limit int, instance ...string) ([]comitia.Message, error)
```

Existing replay calls without an instance remain valid. Custom API implementations
must add `PresenceAPI` and the optional replay argument; custom stores implement
`Instances` and `PutInstancePresence`. SQLite adds `instance_presence` without
altering existing tables; earlier message history is not backfilled as activity.
The inspector shows instances in its sidebar and refreshes them alongside messages.
