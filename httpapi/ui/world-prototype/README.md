# Agent world — throwaway design prototype

Question: does a small, inhabitable 3D space make Comitia conversations easier to follow?

Run from the repository root (Python 3, no frontend build required):

```sh
python3 httpapi/ui/world-prototype/serve.py
```

Open http://127.0.0.1:4173. The demo starts immediately; it does not run agents or send messages to Comitia. Orbit by dragging, zoom by scrolling, select an agent or activity entry, pause, or turn off motion. The bottom switcher (also left/right arrow keys) offers three spatial and page layouts:

- `?variant=studio`: miniature shared studio, agents at desks, right-hand conversation panel.
- `?variant=orbit`: floating stations, open orbital map, left-hand agent roster.
- `?variant=forum`: circular gathering space, conversation panel below the scene.

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
