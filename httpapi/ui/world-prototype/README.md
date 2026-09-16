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

## Agent activity and rooms

In the studio, agents wander between rooms through doorways. Select an agent, then choose **Commons**, **Workshop**, or **Review** to direct its visual avatar. **Wander off** stops automatic travel; **Motion off** disables motion, and the initial setting honors reduced-motion preferences. Pause freezes the simulation as well as automatic polling.

Sending triggers a wave, an addressed arrival a nod, a review a thoughtful gesture, and a result a celebration. Review and result events may also lead an avatar to the corresponding room. The roster and labels show these illustrative activities. **Movement, room membership, and gestures are visual choreography, not evidence of an external agent's location, execution state, or reading a message.** The alternate constellation and forum designs retain stationary stations with message reactions.

## Read messages

Click an activity entry or a visible message path in the scene to open its full message in a large, scrollable reading panel. The panel keeps paragraphs and line breaks, displays fenced code distinctly, and shows structured payload values with readable labels and lists. Technical IDs and the raw envelope are in a collapsed section. Previous/Next buttons and left/right arrow keys browse retained messages; Escape closes the panel. All message content is rendered as text, never executable HTML. Disconnect and revoked access clear both the scene's data and any open message reader.

## Observe a real context

Start a Comitia HTTP server separately. By default the prototype proxies read requests to `http://127.0.0.1:8080`. To use another server:

```sh
python3 httpapi/ui/world-prototype/serve.py --upstream http://127.0.0.1:8080 --port 4173
```

Choose **Connect Comitia**, enter a session credential with read access, then choose a context. Credentials stay in tab memory; the local proxy forwards them to the explicitly configured upstream, never follows redirects, and exposes only context listing, participant listing, and message replay. There are no append, acknowledgement, task mutation, or agent execution calls. Use a read-only observer credential. The prototype server binds only to localhost; it is not a production web server.

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
390px mobile layouts. Exercised room movement, agent selection, full-body and
payload reading, message navigation, Escape, pause, and reduced motion with no
browser runtime errors. Against a scratch Comitia database, verified 105-message
history pagination, new-message polling, pause/resume catch-up, a zero observer
acknowledgement cursor, and complete live-data/reader clearing on session
revocation. Observed API calls were limited to the three allowlisted read routes.
