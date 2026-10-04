# Conversation inspector

The inspector is a read-only adapter over the existing Go application API. The
HTTP server serves embedded, dependency-free HTML/CSS/JavaScript at `/ui/`; all
JSON data comes from the authenticated `/v1/` endpoints. Browser requests reject
redirects, omit ambient cookies, and use a credential entered by the observer.
No server owner credential is embedded or shared with browsers.

The interface has context/task navigation, a chronological conversation view,
and a message inspector. An interaction map aggregates directed routes over loaded
messages: one edge per unique sender/recipient pair, with a separate broadcast
node for messages without explicit recipients. Reciprocal arrows remain separate;
self-addressed messages use loops. Nodes and routes filter the timeline, and selected
messages highlight their routes. The graph follows task/type/search filters; agent
and route focus do not recalculate counts. A keyboard-accessible, paginated connection
list exposes every route. Dense diagrams show up to 16 sessions with an explicit
limit notice; agent focus includes the selected session and its neighbors.
Names are display labels; full session IDs remain
available in details. Addressed messages remain shared within their context.
Unknown/extension schemas show their name and raw JSON without executing content.
References are displayed as text; HTTP(S) references are opened only on user
interaction. Text is rendered through DOM text nodes, never interpreted as HTML.

Discovery is a new library/HTTP/CLI use case. Storage yields candidate contexts
for the caller's session. The application layer reuses effective read-grant
checks, including expiry and revocation. Cursor pagination sorts by context ID
and uses the last readable returned ID, so unauthorized candidates never appear
in the response or cause an early empty page. There is no administrative bypass.

Replay uses a local browser cursor independent of durable acknowledgement
cursors. Each context selection/disconnect increments a generation and aborts
in-flight conversation requests; stale results cannot replace another context's
state. Automatic polling is bounded to 100 messages per tick; memory and DOM
windows are explicitly limited and described in the interface. Metadata is
refreshed alongside messages, including when no new messages arrive. The sidebar
lists self-reported instances from `instances/list`, with relative last-seen times
and expandable resume metadata. Rows whose credential session is revoked or
expired are marked “Session revoked or expired”. Hover over a last-seen time for
its timestamp.
Presence is historical activity, not proof that a process is still running.
Switching contexts or losing read access clears the displayed instance data.
If the presence endpoint is unavailable or returns malformed metadata, messages
continue to render; the sidebar clears old instance rows and shows “Instances
unavailable” until a later refresh succeeds. Message access failures still clear
the conversation.

## Context tree

The sidebar shows readable contexts as a tree: each child under its parent, with
the title of the parent task it serves when the parent's tasks are readable.
Contexts whose parent is not readable start their own top-level branch. Opening a
context also requests its `contexts/children` metadata, which needs read on that
context only. A listed child is readable when it is discovered with its own grant
or is `parent_readable` under a readable parent (the server's read inheritance);
every other child is a locked, non-interactive entry showing only its name,
creator and task. The inspector never requests messages, tasks or participants
of a locked child. Children are paged up to 5,000 per parent; beyond that the
parent notes that more child contexts are not shown. Archived contexts carry an
"Archived" marker in the tree and their archive time in the header.

On a context with children or `comitia.task-status.v1` reports, a "Task progress"
section lists each task with its latest loaded report, the number of loaded
reports and its child context: a link when readable, a locked note otherwise.
Selecting a task filters the conversation to it. Reports are read from the
loaded message window only. Polling rebuilds the tree and task progress only
when their content changes, so keyboard focus stays put between updates.

## Agent timeline

Event order is the default spacing: each addressed route has its own column,
with large sender and recipient occurrence circles linked by a thick direct
arrow. Agents repeat for each exchange; recipient circles are offset to the
right solely for readability, not to represent a receipt timestamp.
labelled with the message's log sequence number and send time. Distinct events
remain separated even when timestamps coincide. Multiple recipients repeat the
same sequence number. Horizontal distance in this mode is not elapsed time;
choose Send time for proportional timestamp spacing.

The agent timeline shows addressed messages on session lanes against send time.
Arrows identify intended recipients, not delivery latency or proof of reading.
Broadcasts have an explicit shared destination; they are not expanded into
invented individual readers. Selecting a message opens its existing details and
reply navigation. Task, agent, type and text filters help isolate conversations.

The timeline operates on the loaded replay window, not the entire database.
Bounded diagram pages keep dense conversations navigable; use the message list
and recipient details when an exchange extends beyond the visible lanes.
Server-side time-range aggregation and recorded read receipts remain future
work. Inspecting an arrow never advances an agent's acknowledgement cursor.

## Future execution view

Keep imported execution events separate from the authoritative coordination log.
A future runner adapter can attach externally visible prompts, responses, tool
calls/results, and explicit summaries using context, session and task IDs plus a
stable run ID and per-run event sequence. The UI could then link a coordination
finding to the execution that produced it. This is a design direction, not an
implemented ingestion contract. Import requires an explicit runner integration,
access control, retention policy and handling of secrets in tool outputs.
Private internal model reasoning is not an available transcript source.

## Validation

Go tests cover context scoping, cursor pagination through unreadable candidates,
revocation, credential expiry, HTTP client parity, and static asset restrictions.
Node's built-in tests cover stale generations, deduplication, replay cursors,
filter combinations, nested reply membership, the context tree, locked children
(never requested) and archived markers. Existing race, crash recovery,
and multi-process checks continue to run in CI. No frontend packages are needed.

## Using the inspector

Run `comitia serve --listen 127.0.0.1:8080` and open
[the inspector](http://127.0.0.1:8080/ui/). It is embedded in the Go binary;
no frontend build, Node installation, or separate hosting service is needed.
Enter a session credential with read grants for the contexts you want to inspect.
For an observer-only session, delegate with `--permissions read`.

The inspector lists readable contexts, tasks, and agent conversations. Its interaction
map shows session nodes and directed, counted connections. Click a node or arrow
to focus the exchange, or use the keyboard-accessible connection list. Broadcasts
have a separate shared-context node; they are not inferred private deliveries. Filter
loaded messages by sender/recipient session, task, message type, or text; follow
reply threads and inspect structured payloads and evidence references. Task
status comes from the task API, separately from agents' reported results.
Messages update by polling every three seconds. Pause stops automatic polling;
manual refresh remains available. Browsing never acknowledges messages or changes
task state.

Credentials are kept in the tab's session storage so refreshing reconnects
automatically. Disconnecting or losing authorization clears the saved credential;
it is never placed in URLs or local storage. Serve remote deployments behind HTTPS.
The shell is public; all conversation data uses the authenticated API. Expired or
revoked read access clears the inspector's loaded data on the next failed request.

Context discovery is paginated: `comitia context-list --limit 100 --after-id ID`
or HTTP `POST /v1/contexts/list` with `{"after_id":"", "limit":100}`.
Use the last returned context ID as the next cursor. Only effective read grants
are included, including for administrators. Custom `ReadStore` implementations
must provide `ContextCandidates(sessionID, afterID, limit)`; the service checks
effective grants before returning candidates to callers.

Filters and thread links cover loaded history, not an implicit global search.
Replay loads up to 100 messages per refresh and keeps at most 5,000 per window;
continue to the next window for longer conversations. Up to 200 matches render
at once; use Earlier/Later matches or filters to browse other loaded messages. Task and active participant
lists are bounded to 1,000; older or expired senders retain their session IDs even
when their display names cannot be resolved. Referenced tasks beyond the list
can be fetched from message details.

The inspector shows coordination messages and any structured execution-event
messages that runners publish. It does not capture external transcripts, tool
output, or agent reasoning. Private internal reasoning is not recorded.
