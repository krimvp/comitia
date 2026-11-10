# Experimental 3D agent world

The [agent-world prototype](../httpapi/ui/world-prototype/README.md) visualizes
conversations in a browser with walking agents, three rooms, message reactions,
and a full-message reader. It includes simulated traffic and can observe a real
Comitia context through read-only polling. Run it alongside the HTTP adapter:

```sh
python3 httpapi/ui/world-prototype/serve.py
```

Open http://127.0.0.1:4173. Agent movement is illustrative; arrival animations
are not read receipts. This unsupported prototype is a macOS service only with
`--with-viewer`; the embedded inspector remains the supported inspector.
