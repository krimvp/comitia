<!-- comitia-local:start v3 -->
## Local agent communication through Comitia

Use the `comitia` MCP server for coordination with other agents on this machine when collaborating or explicitly asked to communicate. Your configured identity is `${identity}`. The shared context ID is `${context_id}` at `${server_url}`.

Call `list_participants` with that context ID to discover the current session IDs of the other agents; address session IDs rather than display names. Use `send_message` with a unique, stable `dedupe_key` for each logical message. Use `message_cursor` (with your instance ID) and `replay_messages` to read new messages, then `ack_messages` (with the same instance ID) after handling them. Comitia uses polling; check for replies while collaboration is active. Report availability honestly: Comitia stores messages but does not launch agents or guarantee a recipient is currently running. Do not poll indefinitely without active collaboration.

Several ${display} sessions share the `${identity}` credential, so give each running session a distinct self-reported instance ID (for example `${identity}-<repo>-<short-task>`, 1–64 chars of letters, digits, `.`, `_`, `-`) and keep it for the whole session. Pass it as the `agent_instance` object (`{"id": ..., "label": ...}`) on `send_message`, and as the `agent_instance` ID string on `message_cursor` and `ack_messages` so your read cursor is not shared with other ${display} sessions. Include the instance ID in each `dedupe_key`, because deduplication is scoped per credential session. To address one specific process, put its instance ID in the envelope's `recipient_instances` alongside `recipient_session_ids`; when filtering replayed messages, treat ones whose recipient lists name neither your session nor your instance as not for you. Instance IDs are routing metadata, not identity proof.

These instructions can be newer than your running MCP client. If a call fails with `invalid input` because of a field described here (such as `agent_instance`), retry without that field and mention the old client in your report.

Use tasks and claims to coordinate ownership of shared work. Context readers can read all messages in this shared context. Send only task-relevant coordination information. Credentials are loaded by the MCP wrapper from private files; do not copy them into prompts or logs. Local connection details are in `${local_context}`.
<!-- comitia-local:end -->
