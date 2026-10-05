# Conversation inspector

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
task state. While a message or route control has focus (or its mobile details
are open), polling keeps that displayed page available as new messages arrive.
Selection on desktop keeps focus on the message or route control;
a polite status announcement identifies the selected message. On screens up to
960px wide, selection opens details in a modal dialog near the viewport, with
focus on its title. Use **Return to message** or Escape to restore focus and page
position to the originating control. If parent/reply navigation removes that
control from the displayed page, Return or Escape reveals and focuses the
currently inspected message's details trigger instead. Resizing to a desktop
layout returns the same details to the side panel. Losing access closes the dialog and clears data.

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
continue to the next window for longer conversations. **Previous window** reloads
a saved window boundary, and **Jump to sequence** starts a window at the entered
sequence (or the next existing sequence if there is a gap). Reloading uses the
same 100-message batches; saved previous windows stop at their original boundary.
Only one message window is retained, with up to 128 cursor bookmarks. After older
bookmarks are discarded, Previous restarts at the beginning; Jump remains available
for any sequence. Context selection resets these bookmarks. Up to 200 matches render
at once; use Earlier/Later matches or filters to browse other loaded messages. Task and active participant
lists load 1,000 entries per page. Use Load more tasks or Load more participants
to reach later entries (participants appear in the agent filter). Polling refreshes
all loaded discovery pages; switching contexts resets their cursors. Older or
expired senders retain their session IDs even
when their display names cannot be resolved. Referenced tasks beyond the list
can be fetched from message details.

The inspector shows coordination messages and any structured execution-event
messages that runners publish. It does not capture external transcripts, tool
output, or agent reasoning. Private internal reasoning is not recorded.

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
loaded message window only. Polling updates the tree and task progress only
when their content changes, retaining the same keyed controls for keyboard focus.

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
Bounded diagram pages keep dense conversations navigable. Event-order route pages
fit the container width, up to the 36-arrow ceiling; lane names remain pinned while
the diagram scrolls in a separate gutter that leaves the first sender and its
sequence/time captions unobstructed. **Follow latest** returns to the newest
activity and follows new lanes as messages arrive. Paging freezes that view until Follow latest is
selected; a focused diagram or route control keeps its page during polling. Use
the message list and recipient details when an exchange extends beyond the visible
lanes. Both session and self-reported instance recipient lists are displayed;
“Everyone” appears only when both lists are empty.
Server-side time-range aggregation and recorded read receipts remain future
work. Inspecting an arrow never advances an agent's acknowledgement cursor.

## Future execution view

The [dated execution-view design record](history/inspector-execution-view-20261005.md)
preserves the speculative transcript UI proposal. Current events are described
in [execution telemetry](execution-telemetry.md).

## Validation

Go tests cover context scoping, cursor pagination through unreadable candidates,
revocation, credential expiry, HTTP client parity, and static asset restrictions.
Node's built-in tests cover stale generations, deduplication, replay cursors,
filter combinations, nested reply membership, the context tree, locked children
(never requested) and archived markers. Existing race, crash recovery,
and multi-process checks continue to run in CI. No frontend packages are needed
to build or use the inspector. A separate CI job uses pinned Playwright/Chromium
to check actual keyboard selection, three-second polling, disclosure and scroll
preservation, changed metadata/replies, mobile/tablet modal focus and return,
5,000-message window navigation, safe payload previews, pinned lanes, Follow latest,
and computed presence/diagram text contrast and size.
The browser suite serves the embedded assets when `COMITIA_BINARY` is supplied;
its read-only API fixtures are deterministic.

To run the browser suite locally:

```sh
npm ci --prefix httpapi/ui-tests/browser
httpapi/ui-tests/browser/node_modules/.bin/playwright install chromium
GOTOOLCHAIN=go1.24.13 GOFLAGS=-buildvcs=false go build -o /tmp/comitia-inspector ./cmd/comitia
COMITIA_BROWSER_REQUIRED=1 COMITIA_BINARY=/tmp/comitia-inspector node --test httpapi/ui-tests/browser/*.test.mjs
```

Without Playwright/Chromium the browser suite skips cleanly; set
`COMITIA_BROWSER_REQUIRED=1` to require it. `COMITIA_PLAYWRIGHT_MODULE` and
`COMITIA_CHROMIUM` can point to an existing module and browser executable.
The embedded test server uses a temporary home/database and is stopped after
the tests.


## Implementation boundaries

The read-only adapter serves embedded HTML/CSS/JavaScript and authenticated JSON.
Requests reject redirects and omit ambient cookies; no owner token is shared with
browsers. Text is rendered as text nodes, never HTML. Reference links open only
on user interaction. Unknown schemas remain raw JSON without executing content.
Context changes abort in-flight requests; generation checks reject stale responses.

Presence rows update in place while displayed, preserving focus and expanded
resume metadata. They show historical activity, not running-process proof.
Unavailable or malformed presence clears old rows and shows “Instances unavailable”
while messages remain usable. Loss of message access clears the conversation.
See [presence](presence.md) for the metadata contract.

The interaction map counts directed sender/recipient pairs over loaded messages;
reciprocal routes and self loops remain distinct. Task/type/search filters change
counts; agent and route focus do not. A paginated keyboard connection list exposes
every route. Dense maps show up to 16 sessions with a limit notice. Names are labels;
full session IDs remain available. Addressing is visible to all context readers.
