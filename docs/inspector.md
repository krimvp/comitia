# Conversation inspector

The inspector is a read-only adapter over the existing Go application API. The
HTTP server serves embedded, dependency-free HTML/CSS/JavaScript at `/ui/`; all
JSON data comes from the authenticated `/v1/` endpoints. Browser requests reject
redirects, omit ambient cookies, and use a credential entered by the observer.
No server owner credential is embedded or shared with browsers.

The interface has context/task navigation, a chronological conversation view,
and a message inspector. Names are display labels; full session IDs remain
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
refreshed alongside messages, including when no new messages arrive.

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
filter combinations, and nested reply membership. Existing race, crash recovery,
and multi-process checks continue to run in CI. No frontend packages are needed.
