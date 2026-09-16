# Agent world — throwaway design prototype

Question: does a small, inhabitable 3D space make Comitia conversations easier to follow?

Run from the repository root (Python 3, no frontend build required):

```sh
python3 httpapi/ui/world-prototype/serve.py
```

Open http://127.0.0.1:4173. The demo starts immediately; it does not run agents or send messages to Comitia. Orbit by dragging, zoom by scrolling, select an agent or activity entry, pause, or turn off motion. The bottom switcher (also left/right arrow keys) offers three spatial and page layouts:

- `?variant=studio`: three-room studio (Commons, Workshop, Review room), walking agents, right-hand conversation panel.
- `?variant=orbit`: floating stations, open orbital map, left-hand agent roster.
- `?variant=forum`: circular gathering space, conversation panel below the scene.

## Purposeful movement and the scene queue

Agents stay in place unless an explicit structured message creates a room action.
There is no random wandering or manual room travel. The routing rules are:

| Message schema | Avatar | Destination |
| --- | --- | --- |
| `comitia.assignment.v1` | Each explicit recipient | Workshop |
| `comitia.review-request.v1` | Each explicit recipient | Review room |
| `comitia.work-submitted.v1` | Sender | Commons |
| `comitia.verification-result.v1` | Sender | Commons |
| `comitia.evaluation-result.v1` | Sender | Commons |

No recipients means no inferred assignment/review participants. Ordinary text,
discussion, and unsupported schemas do not cause room changes. Routing never
uses guessed intent from message text.

The **Messages** view contains observed conversation evidence. The separate
**Scene queue** view explains room actions, their source messages, task IDs, and
visual stages: Queued → Move → Highlight → Shown. Actions run in order for each
agent; different agents can have concurrent actions. A dashed floor path shows
the active route through doorways, and a compact scene summary shows the current
action. Floating labels show names only; stable roster roles replace activity
chatter. The demo is a finite scenario that waits for pending room actions before
automatically emitting another event; **Next demo event** can add one manually.

**These are visualization steps, not a real agent work queue.** “Shown” never
means that work completed, a message was read, or an agent actually changed
execution state. Opening a message does not enqueue it again. Loaded history is
not animated or enqueued. At most 120 actions remain pending, with 40 recently
shown/outside-scene actions retained; excess actions are counted in the queue
notice while their messages remain in the message feed.

**Motion off** preserves the semantic destination and queue order, placing avatars
without animated travel. It honors reduced-motion preferences initially. Pause
freezes scene progression and automatic polling. Alternative layouts retain the
queue and message highlights, without studio room travel.

## Read messages

Click an activity entry or a visible message path in the scene to open its full message in a large, scrollable reading panel. The panel keeps paragraphs and line breaks, displays fenced code distinctly, and shows structured payload values with readable labels and lists. Technical IDs and the raw envelope are in a collapsed section. Previous/Next buttons and left/right arrow keys browse retained messages; Escape closes the panel. All message content is rendered as text, never executable HTML. Disconnect and revoked access clear both the scene's data and any open message reader.

## Observe a real context

Start a Comitia HTTP server separately. By default the prototype proxies read requests to `http://127.0.0.1:8080`. To use another server:

```sh
python3 httpapi/ui/world-prototype/serve.py --upstream http://127.0.0.1:8080 --port 4173
```

Choose **Connect Comitia** (or the matching option in the header context selector), enter a session credential with read access, then choose a context. Credentials stay in tab memory; the local proxy forwards them to the explicitly configured upstream, never follows redirects, and exposes only context listing, participant listing, and message replay. There are no append, acknowledgement, task mutation, or agent execution calls. Use a read-only observer credential. The prototype server binds only to localhost; it is not a production web server.

The persistent **Context** selector lists every context readable by the current credential, with paginated discovery and short IDs to distinguish duplicate names. Switch conversations without reconnecting; each switch cancels old requests and clears the old agents, messages, selection, open reader, and scene queue. The Messages view opens for the new context. Use **↻** beside the selector to discover newly available contexts. If read access to one context disappears, its data is cleared while the remaining contexts stay selectable; an invalid or expired session disconnects entirely.

The browser loads history without animation, then polls every second and animates newly observed messages. At most 100 messages are loaded per request, with faster pagination while catching up. It retains the latest 1,000 messages, displays the latest 60 matching messages, lists up to 1,000 current participants, and renders at most 24 agents. The feed still includes messages involving agents outside the scene. Permission failures clear live data and return to the clearly labeled demo. Disconnect clears the credential and live data.

## Meaning of the animation

- A sender reacts when a new durable message is observed.
- A traveling packet shows `recipient_session_ids` routing metadata.
- The destination reaction illustrates an addressed message. It **does not prove receipt, replay, reading, or acknowledgement** by that agent.
- Messages with no recipients travel to the shared-context beacon. Broadcast delivery to each agent is not inferred.
- Message type colors distinguish ordinary messages, reviews, and results. Results are messages, not proof of task completion.
- Participant labels reflect read access or presence in observed history, not online/running state.
- Replay is polling, not a push stream. True recipient-received animation would require explicit consumer telemetry or another observer-visible receipt event.

The prototype is adjacent to the existing inspector but is deliberately served separately. It does not change the embedded inspector or its security policy. Three.js 0.180.0 and OrbitControls are vendored under their MIT license. Fonts are optional Google Fonts, with local fallbacks.

## Decision to capture

Pending user review: choose the spatial layout, agent appearance, and how much message detail should remain visible. Absorb the chosen design into the inspector and remove unused variants before treating this as production UI. Production work should include server integration, asset bundling/CSP, larger-context layout, receipt telemetry if desired, and browser/accessibility coverage.

## Validation performed

Manually inspected browser renders for the studio, constellation, forum, and
390px mobile layouts. Exercised message-driven room movement, agent selection, full-body and
payload reading, message navigation, Escape, pause, and reduced motion with no
browser runtime errors. Against a scratch Comitia database, verified 105-message
history pagination, new-message polling, pause/resume catch-up, a zero observer
acknowledgement cursor, and complete live-data/reader clearing on session
revocation. Observed API calls were limited to the three allowlisted read routes.

Queue-specific browser checks also verified that idle agents remain stationary,
ordinary discussions create no room actions, two actions for one agent execute
in order, source-message links open the full reader, and the 390px layout has no
horizontal overflow. The queue and moving scene rendered without browser errors.

Context-selector checks used three scratch contexts: switching isolated their
messages, cleared the reader and queue, discovered a newly created context on
refresh, handled an empty context, and survived rapid changes without showing
old messages. Desktop and mobile selectors rendered without overflow or browser
runtime errors. These checks did not use or modify a real user conversation.
