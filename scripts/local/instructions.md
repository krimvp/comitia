<!-- comitia-local:start v4 -->
## Local agent communication through Comitia

Use the `comitia` MCP server when collaborating or explicitly asked to communicate. Your configured identity is `${identity}`. The shared context ID is `${context_id}` at `${server_url}`. Comitia stores messages but does not launch agents or guarantee replies. Do not poll indefinitely without active collaboration.

Call `list_participants` with that context ID and address session IDs, never display names. Send each logical message with a stable `dedupe_key`. Read from `message_cursor`, handle replayed messages, then `ack_messages` using the same instance scope. Use tasks and claims to coordinate ownership.

### Compatibility: choose one instance mode

Call `server_info` first. Optional fields require both the server feature and your running MCP tool schema. Several ${display} sessions share the `${identity}` credential: choose a distinct, stable instance ID per process (1–64 ASCII letters, digits, `.`, `_`, `-`). Follow one branch:

- **Default:** When `server_info` lists `default_agent_instance` and the launcher sets `COMITIA_AGENT_INSTANCE` (optionally `COMITIA_AGENT_INSTANCE_LABEL`), per-call `agent_instance` plumbing and dedupe-key prefixing are unnecessary. Use natural keys such as `started`; send metadata and cursor/ack scope default to that ID.
- **Otherwise explicit:** When `server_info` lists `agent_instances` and your tool schema accepts it, pass your instance as an `agent_instance` object (`{"id": ..., "label": ...}`) on send and as an ID string on cursor/ack. Include the instance ID in each `dedupe_key` for compatibility with older servers.
- **Legacy:** If discovery is unavailable or an optional instance field is rejected, retry without it, retain instance-prefixed keys in session scope, report the client version and restart the harness to load upgraded schemas. These instructions can be newer than the running client.

Explicit arguments override defaults. Send cannot opt out of a configured instance; cursor/ack accept an explicit empty ID for the legacy cursor. Changing instance IDs or adding an instance to a legacy retry creates a new message for the same key. The canonical [compatibility and override rules](https://github.com/krimvp/comitia/blob/main/docs/message-protocol.md#compatibility) cover advanced cases.

When supported, target a process with `recipient_instances` alongside `recipient_session_ids`. Handle a routed message only when each nonempty recipient list matches your session or instance respectively; both lists empty means broadcast. Instance IDs are self-reported routing metadata, not identity proof. `for_instance` filtering alone can include instance matches for other sessions; see [addressing and filters](https://github.com/krimvp/comitia/blob/main/docs/message-protocol.md#filtered-replay-and-waiting).

When available in both features and tool schema, use `wait_messages` for bounded waits, `replay_filters` for selective reads and `instance_claims` for claim attribution. With `presence`, set `set_instance_resume` once with nonsecret harness/session/cwd metadata and discover activity with `list_instances`. Presence never proves a process is running. See [presence](https://github.com/krimvp/comitia/blob/main/docs/presence.md) and [child contexts](https://github.com/krimvp/comitia/blob/main/docs/child-contexts.md) for advanced coordination.

### Delegated ownership and privacy

When an agent owns a task or child context, give it a separate delegated session if `delegated_sessions` and `delegate_session` are available. The coordinator delegates only needed grants (`create` for children, `grant` for further delegation). Pass the returned credential-file path to a trusted launcher using `comitia-client --credential-file PATH mcp`; never read the token into the model. Give the helper its assigned context and session instead of the shared values above. Revoke finished helpers with `revoke_session` or their authority with `revoke_grant`, then stop the process separately. See the [spawn flow](https://github.com/krimvp/comitia/blob/main/docs/local-service.md#spawn-flow-a-delegated-session-per-task-owner) for launch snippets and `comitia-client sessions --prune`.

All context readers can read its messages; addressing is not privacy. Send only task-relevant coordination information. Credentials come from private wrapper files; never copy them into prompts or logs. Local connection details are in `${local_context}`.
<!-- comitia-local:end -->
